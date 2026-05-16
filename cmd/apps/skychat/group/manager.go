// Package group — cmd/apps/skychat/group/manager.go: orchestrates
// this visor's collection of group sessions, parallel to pairing.Manager.
//
// The Manager owns the bbolt Store and a map[groupID]*Session of
// live runtime state. It glues persistence to lifecycle:
//
//   - Create: owner-side; allocates port + AES key (private mode),
//     persists the record, opens the publisher.
//   - Join: member-side; takes an invite, persists the record,
//     opens the subscriber + Connect.
//   - Leave: member-side; transitions to StatusLeft and tears the
//     session down.
//   - Delete: owner-side; revokes the record, tears down.
//   - Resume: on startup, walk the store and reopen everything
//     that's still active.
//   - SendToGroup: owner-side relay path. Member-side outgoing is
//     handled by skychat's existing 1:1 wire with a group_id tag;
//     the skychat app calls this on the owner side after receiving
//     such a message.
//
// Message fan-out: a single MessageHandler is installed at Manager
// init and propagated to every Session, so skychat's hub just
// receives a (groupID, senderPK, msg) tuple and turns it into an
// SSE event.
package group

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// Manager owns the persistent record set and live Session instances.
// Safe for concurrent use.
type Manager struct {
	store   *Store
	dmsgC   *dmsg.Client
	myPK    cipher.PubKey
	mySK    cipher.SecKey
	dataDir string
	log     *logging.Logger

	// portAlloc decides which DMSG port to assign to a brand-new
	// owner-side group. Defaults to a random pick from a reserved
	// range; tests override it.
	portAlloc func() (uint16, error)

	onMessageMu sync.RWMutex
	onMessage   MessageHandler

	mu       sync.RWMutex
	sessions map[string]*Session

	// heartbeatInterval is the owner-emit cadence forwarded to every
	// new owner-role Session via openLocked. Zero disables.
	heartbeatInterval time.Duration

	// reconnect loop state — see runReconnectLoop.
	reconnectCtx    context.Context
	reconnectCancel context.CancelFunc
	reconnectWG     sync.WaitGroup
	reconnectMu     sync.Mutex
	reconnectState  map[string]*reconnectState // keyed by group ID
	// peerReconnectState tracks per-(group, peer) backoff for the
	// tryReconnectPeers path so one wedged peer's failures don't
	// engage a session-level backoff that locks out healthy peers'
	// reconnect attempts. Parallel to reconnectState; the session-
	// level map still backs tryReconnect (full Connect) and
	// tryReconnectLegacySub. Outer key = group ID, inner key = peer PK.
	peerReconnectState map[string]map[cipher.PubKey]*reconnectState
}

// reconnectState tracks per-group consecutive-failure backoff so a
// permanently-unreachable owner doesn't get hammered every cycle.
// See runReconnectLoop for the state machine.
type reconnectState struct {
	failures uint32
	// nextAttempt is set when a backoff transition extends the
	// per-group interval beyond the base 30s tick. The reconnect
	// loop skips groups whose nextAttempt is still in the future.
	nextAttempt time.Time
}

// ManagerConfig wires a Manager.
type ManagerConfig struct {
	Store   *Store
	DmsgC   *dmsg.Client
	MyPK    cipher.PubKey
	MySK    cipher.SecKey
	DataDir string
	Logger  *logging.Logger

	// HeartbeatInterval, when > 0, makes every owner-role session
	// opened by this Manager emit a periodic no-op heartbeat probe.
	// Members observe these to detect a silently-stalled CXO
	// subscriber inside ~3×interval. Zero disables (default for
	// callers that don't set it; for visor production set to 30s).
	HeartbeatInterval time.Duration
}

// DefaultHeartbeatInterval is the recommended interval an owner
// session emits its heartbeat probe. 30s gives a stall-detection
// window of ~90s without measurable wire overhead on a typical
// group.
const DefaultHeartbeatInterval = 30 * time.Second

// NewManager constructs a manager. Does not open any sessions; call
// Resume to bring up everything in the store.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.Store == nil {
		return nil, errors.New("group: NewManager: Store required")
	}
	if cfg.DmsgC == nil {
		return nil, errors.New("group: NewManager: DmsgC required")
	}
	if cfg.DataDir == "" {
		return nil, errors.New("group: NewManager: DataDir required")
	}
	log := cfg.Logger
	if log == nil {
		log = logging.MustGetLogger("skychat-group-manager")
	}
	return &Manager{
		store:              cfg.Store,
		dmsgC:              cfg.DmsgC,
		myPK:               cfg.MyPK,
		mySK:               cfg.MySK,
		dataDir:            cfg.DataDir,
		log:                log,
		portAlloc:          defaultPortAlloc,
		sessions:           make(map[string]*Session),
		heartbeatInterval:  cfg.HeartbeatInterval,
		reconnectState:     make(map[string]*reconnectState),
		peerReconnectState: make(map[string]map[cipher.PubKey]*reconnectState),
	}, nil
}

// SetMessageHandler installs the inbound callback. Propagated to
// every live Session.
func (m *Manager) SetMessageHandler(h MessageHandler) {
	m.onMessageMu.Lock()
	m.onMessage = h
	m.onMessageMu.Unlock()
	m.mu.RLock()
	for _, s := range m.sessions {
		s.SetMessageHandler(h)
	}
	m.mu.RUnlock()
}

// Create constructs a new owner-side group, persists it, and opens
// the publisher. Returns the persisted Record (with ID + Port + key
// populated) so the caller can build the invite link.
func (m *Manager) Create(name string, mode Mode, initialMembers []cipher.PubKey) (Record, error) {
	if name == "" {
		return Record{}, errors.New("group: Create: name required")
	}
	if !mode.IsValid() {
		return Record{}, fmt.Errorf("group: Create: invalid mode %q", mode)
	}
	port, err := m.portAlloc()
	if err != nil {
		return Record{}, fmt.Errorf("group: Create: port: %w", err)
	}
	r := Record{
		ID:        uuid.NewString(),
		Name:      name,
		OwnerPK:   m.myPK,
		Port:      port,
		Mode:      mode,
		Members:   uniqueWithSelf(m.myPK, initialMembers),
		Role:      RoleOwner,
		Status:    StatusActive,
		CreatedAt: time.Now().UTC(),
		JoinedAt:  time.Now().UTC(),
	}
	if mode == ModePrivate {
		key, err := GenerateAESKey()
		if err != nil {
			return Record{}, err
		}
		r.AESKey = key
	}
	if err := m.store.Put(r); err != nil {
		return Record{}, fmt.Errorf("group: Create: store: %w", err)
	}
	if _, err := m.openLocked(r); err != nil {
		_ = m.store.Delete(r.ID) //nolint:errcheck
		return Record{}, err
	}
	return r, nil
}

// Join accepts an invite link, persists a member-side record, and
// opens a subscriber connected to the owner. Returns the record.
func (m *Manager) Join(inv Invite) (Record, error) {
	if inv.OwnerPK == m.myPK {
		return Record{}, errors.New("group: Join: refusing to subscribe to own group")
	}
	r := Record{
		ID:        inv.ID,
		Name:      inv.Name,
		OwnerPK:   inv.OwnerPK,
		Port:      inv.Port,
		Mode:      inv.Mode,
		AESKey:    inv.AESKey,
		Members:   []cipher.PubKey{inv.OwnerPK, m.myPK},
		Role:      RoleMember,
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
		JoinedAt:  time.Now().UTC(),
	}
	if err := m.store.Put(r); err != nil {
		return Record{}, fmt.Errorf("group: Join: store: %w", err)
	}
	sess, err := m.openLocked(r)
	if err != nil {
		_ = m.store.Delete(r.ID) //nolint:errcheck
		return Record{}, err
	}
	// Join's interactive caller wants a bounded wait: a Connect that
	// blocks indefinitely on an unreachable owner publisher would
	// stall the CLI. Use reconnectAttemptTimeout — same per-attempt
	// budget the background reconnect loop uses, so the failure
	// mode is identical (and the same backoff machinery picks up
	// on the next 30s tick).
	joinCtx, cancel := context.WithTimeout(context.Background(), reconnectAttemptTimeout)
	defer cancel()
	if err := sess.Connect(joinCtx); err != nil {
		_ = sess.Close()         //nolint:errcheck
		_ = m.store.Delete(r.ID) //nolint:errcheck
		return Record{}, fmt.Errorf("group: Join: connect: %w", err)
	}
	_ = m.store.SetStatus(r.ID, StatusActive) //nolint:errcheck
	r.Status = StatusActive
	return r, nil
}

// Leave (member) or Delete (owner): both tear down the session and
// move the record to a terminal status. Idempotent on a missing or
// already-terminal record.
func (m *Manager) Leave(id string) error { return m.terminate(id, StatusLeft) }

// Delete tears down an admin-side group and marks it revoked. Only
// visors that IsAdmin on the record may delete; non-admin members
// should call Leave instead. Returns an error for the wrong-role
// case so a typo doesn't silently revoke a group that another
// admin still owns.
func (m *Manager) Delete(id string) error {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return fmt.Errorf("group: Delete: get: %w", err)
	}
	if !ok {
		return nil // already gone — idempotent, matches terminate's contract
	}
	if !r.IsAdmin(m.myPK) {
		return errors.New("group: Delete: only admins can revoke a group; use Leave to depart")
	}
	return m.terminate(id, StatusRevoked)
}

func (m *Manager) terminate(id string, status Status) error {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return fmt.Errorf("group: terminate: get %s: %w", id, err)
	}
	if !ok {
		return nil // already gone
	}
	m.mu.Lock()
	sess, live := m.sessions[id]
	if live {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if live {
		_ = sess.Close() //nolint:errcheck
	}
	// Drop all per-peer reconnect state for this group — the session
	// (and its peerSubs) are gone, so any remaining (id, peer) entries
	// in peerReconnectState would be unreachable memory until next
	// process restart.
	m.reconnectMu.Lock()
	delete(m.reconnectState, id)
	delete(m.peerReconnectState, id)
	m.reconnectMu.Unlock()
	_ = m.store.SetStatus(r.ID, status) //nolint:errcheck
	return nil
}

// SendToGroup publishes a message into the named group's CXO feed.
// Dispatches by role:
//
//   - Owner: writes directly to the publisher via Session.Send.
//   - Member: opens a dmsg stream to the owner's relay listener
//     and submits the message; the owner re-publishes into the
//     feed with sender attribution preserved.
//
// In both cases the sender's own subscriber sees the resulting
// feed leaf and renders it to the inbox, so the UX is "type, hit
// Send, see your message in history" regardless of role.
func (m *Manager) SendToGroup(ctx context.Context, id, text string) error {
	m.mu.RLock()
	sess, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("group: SendToGroup: no live session for %s", id)
	}
	// Federated send: every member — owner OR member — publishes to
	// their OWN feed. Other members observe directly via peerSubs.
	// The relay-to-owner path (sess.SubmitToOwner) is retained as a
	// fallback only when the local visor's publisher isn't healthy
	// at send time; for steady-state groups every Send is direct.
	//
	// Pre-federated v1 routed every member message through the owner
	// as a single-point-of-failure relay. The session-level
	// infrastructure (per-member publishers, per-peer subscribers) was
	// already in place before this change — the only behavioral
	// difference is the manager's role dispatch here.
	if err := sess.Send(text); err != nil {
		// Fallback for the case where this visor's publisher isn't
		// healthy enough to publish (e.g. CXO node mid-bootstrap).
		// Only meaningful for member-role sessions — owner role has
		// nowhere to fall back to.
		if sess.cfg.Record.Role == RoleMember {
			if relayErr := sess.SubmitToOwner(ctx, text); relayErr == nil {
				m.kickReconnect(ctx, id)
				_ = m.store.MarkMessage(id, time.Now().UTC()) //nolint:errcheck
				return nil
			}
		}
		return err
	}
	_ = ctx //nolint:staticcheck // ctx kept in signature for the relay-fallback branch

	_ = m.store.MarkMessage(id, time.Now().UTC()) //nolint:errcheck
	return nil
}

// AddMember (owner) extends the allowlist + persisted member list.
// Returns the updated record so the caller can re-issue the invite.
func (m *Manager) AddMember(id string, pk cipher.PubKey) (Record, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return Record{}, fmt.Errorf("group: AddMember: get: %w", err)
	}
	if !ok {
		return Record{}, fmt.Errorf("group: AddMember: no record for %s", id)
	}
	// Post-#admin-roles: roster authority lives on the Admins set, not
	// on Role. Role still distinguishes publisher (owner) from
	// subscriber (member) infrastructure at session-open time, but
	// "who can add members" is now any visor whose PK is in
	// r.IsAdmin(). The founder check inside IsAdmin keeps the legacy
	// owner-only behavior on records whose Admins slice is empty.
	if !r.IsAdmin(m.myPK) {
		return Record{}, errors.New("group: AddMember: only admins can edit roster")
	}
	for _, existing := range r.Members {
		if existing == pk {
			return r, nil // already present, idempotent
		}
	}
	r.Members = append(r.Members, pk)
	if err := m.store.Put(r); err != nil {
		return Record{}, fmt.Errorf("group: AddMember: store: %w", err)
	}
	m.mu.RLock()
	sess, live := m.sessions[id]
	m.mu.RUnlock()
	if live {
		_ = sess.SetAllowlist(r.Members) //nolint:errcheck
	}
	return r, nil
}

// PromoteAdmin adds pk to the group's Admins set. Callable by any
// existing admin. Idempotent — promoting an already-admin returns the
// current record without mutating storage. The promoted PK gains
// roster authority (AddMember / BuildInvite / Delete / promote+demote)
// from the moment this call returns successfully on this visor; other
// visors learn of the change through the next out-of-band roster
// gossip (admin-mirror PR adds the on-feed channel).
//
// Note: in the federated send architecture, the promoted PK does not
// need to already be a Member — promotion is a roster-authority grant,
// not a membership change. Practically, an admin who isn't also a
// member is unusual; callers should treat that as a separate AddMember
// step where they want it.
func (m *Manager) PromoteAdmin(id string, pk cipher.PubKey) (Record, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return Record{}, fmt.Errorf("group: PromoteAdmin: get: %w", err)
	}
	if !ok {
		return Record{}, fmt.Errorf("group: PromoteAdmin: no record for %s", id)
	}
	if !r.IsAdmin(m.myPK) {
		return Record{}, errors.New("group: PromoteAdmin: only admins can promote")
	}
	if pk == (cipher.PubKey{}) {
		return Record{}, errors.New("group: PromoteAdmin: pk required")
	}
	if r.IsAdmin(pk) {
		return r, nil // already admin (founder OR explicit) — idempotent
	}
	r.Admins = append(r.Admins, pk)
	r.EnsureFounderInAdmins()
	if err := m.store.Put(r); err != nil {
		return Record{}, fmt.Errorf("group: PromoteAdmin: store: %w", err)
	}
	return r, nil
}

// DemoteAdmin removes pk from the group's Admins set. Refuses to
// demote the founder (r.OwnerPK) — the founder is the immutable
// recovery anchor and demoting them would lock the group out of
// roster recovery if every other admin's visor went offline. Other
// admins MAY demote each other (no protection beyond the founder
// rule); the assumption is that admins trust each other by virtue of
// having been promoted.
//
// Idempotent on a PK that wasn't an admin to begin with — returns the
// current record without mutating storage. Errors only on store
// failure, the founder-demote attempt, and the only-admins-can-demote
// gate.
func (m *Manager) DemoteAdmin(id string, pk cipher.PubKey) (Record, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return Record{}, fmt.Errorf("group: DemoteAdmin: get: %w", err)
	}
	if !ok {
		return Record{}, fmt.Errorf("group: DemoteAdmin: no record for %s", id)
	}
	if !r.IsAdmin(m.myPK) {
		return Record{}, errors.New("group: DemoteAdmin: only admins can demote")
	}
	if pk == (cipher.PubKey{}) {
		return Record{}, errors.New("group: DemoteAdmin: pk required")
	}
	if r.IsFounder(pk) {
		return Record{}, errors.New("group: DemoteAdmin: cannot demote the founder")
	}
	out := r.Admins[:0]
	mutated := false
	for _, a := range r.Admins {
		if a == pk {
			mutated = true
			continue
		}
		out = append(out, a)
	}
	if !mutated {
		return r, nil // nothing to do — pk wasn't in the explicit Admins slice
	}
	r.Admins = out
	r.EnsureFounderInAdmins()
	if err := m.store.Put(r); err != nil {
		return Record{}, fmt.Errorf("group: DemoteAdmin: store: %w", err)
	}
	return r, nil
}

// resumeReplayMessageCap is the max number of historical messages
// per group replayed into the in-memory inbox on Resume. Capped so
// a long-lived group with thousands of messages doesn't blow the
// inbox-ring-buffer or the SSE fan-out queues on startup. 100 is
// enough to give a freshly-restarted operator immediate context
// without forcing them to scrape the persistent CXO tree directly.
const resumeReplayMessageCap = 100

// Resume walks the store and reopens every non-terminal session.
// Records in StatusLeft / StatusRevoked are skipped; the operator
// can `group join <invite>` again to bring them back.
//
// On reopen, the most recent resumeReplayMessageCap messages per
// group are replayed through the registered handler so consumers
// (visor inbox → group listen) see immediate context after a visor
// restart, rather than an empty feed until the next live message
// arrives. The replay drives the same handler path as live messages,
// so order and shape match what a steady-state listener would see.
func (m *Manager) Resume() error {
	all, err := m.store.List()
	if err != nil {
		return fmt.Errorf("group: Resume: list: %w", err)
	}
	for _, r := range all {
		if r.Status == StatusLeft || r.Status == StatusRevoked {
			continue
		}
		if _, err := m.openLocked(r); err != nil {
			m.log.WithError(err).WithField("id", r.ID).
				Warn("group: Resume: skipping session")
			continue
		}
		if r.Role == RoleMember {
			m.mu.RLock()
			sess := m.sessions[r.ID]
			m.mu.RUnlock()
			// Resume runs at visor startup — same bounded budget as
			// the background reconnect tick so one unreachable
			// owner can't stall the whole Resume loop.
			resumeCtx, cancel := context.WithTimeout(context.Background(), reconnectAttemptTimeout)
			err := sess.Connect(resumeCtx)
			cancel()
			if err != nil {
				m.log.WithError(err).WithField("id", r.ID).
					Warn("group: Resume: member subscribe connect (will retry in background)")
				// Mark pending; runReconnectLoop will re-attempt on
				// its 30s cadence (with backoff on repeated failures)
				// until the owner becomes reachable.
				_ = m.store.SetStatus(r.ID, StatusPending) //nolint:errcheck
			}
		}
		m.replaySessionHistory(r.ID)
	}
	m.startReconnectLoop()
	return nil
}

// reconnectInterval is the base cadence the reconnect loop walks
// pending sessions on. Short enough that a peer's chat-app restart
// (which usually completes inside 30s) auto-heals without operator
// intervention; long enough that a permanently-unreachable owner
// isn't dial-hammered.
const reconnectInterval = 30 * time.Second

// subscriberStaleThreshold is the lag (now - Session.LastInbound())
// above which a member session is considered stale enough to warrant
// a reconnect attempt. Set to 3× the owner heartbeat cadence (~30s)
// plus jitter, so a single missed heartbeat doesn't trigger flap but
// two consecutive misses do. Used both by
// Session.IsSubscriberAlive (the operator-visible /status flag) and
// Manager.detectStaleAndReconnect (the background recovery driver).
//
// Post-#unified-liveness: there is now ONE liveness signal
// (Session.lastInboundNs, bumped on every onUpdate event including
// heartbeats), so the previous heartbeat-vs-chat-traffic threshold
// split is gone — heartbeats are bumps of lastInbound just like
// chat messages, and the single threshold applies to both.
const subscriberStaleThreshold = 100 * time.Second

// reconnectAttemptTimeout bounds each per-peer Connect call so the
// reconnect loop can't get stuck on a single slow group while other
// pending groups starve. Post-#2608 the deadline is threaded as a
// context all the way to ps.Connect, so the bound is enforced cleanly
// rather than as a goroutine outer-wait that orphaned its inner work.
//
// Sized for the cold-start case: each per-peer attempt must complete
// dmsg-dial + noise XK handshake + initial subscribe-state exchange
// within this budget. On a freshly-restarted visor with no warmed-up
// dmsg session to the peer's delegated server, the dial alone can
// take ~1-2s; the noise handshake adds another 0.5-1.5s; the
// subscribe-state exchange another 0.5-1s. 5s — the pre-bump value —
// was just-enough on a healthy network and routinely deadline-
// exceeded after a router-reset or a fresh-cold-restart, putting the
// session into the 10-fail backoff schedule for no real reason. 15s
// is comfortable headroom while still small enough that per-peer
// stalls don't dominate the reconnect loop's wall time across a
// group of N=10 members (worst case N*15s = 150s, still inside
// subscriberStaleThreshold's reconnect cadence).
const reconnectAttemptTimeout = 15 * time.Second

// Backoff transition thresholds. After this many consecutive
// failures on a given group, the per-group nextAttempt is bumped
// out so the loop skips it until enough wall time has passed.
const (
	reconnectBackoffFailures1 = 10
	reconnectBackoffInterval1 = 5 * time.Minute
	reconnectBackoffFailures2 = 30
	reconnectBackoffInterval2 = 30 * time.Minute
)

// startReconnectLoop launches the background goroutine that retries
// failed member-side Connects. Safe to call multiple times — the
// second call is a no-op while the loop is already running.
func (m *Manager) startReconnectLoop() {
	m.reconnectMu.Lock()
	if m.reconnectCtx != nil {
		m.reconnectMu.Unlock()
		return
	}
	m.reconnectCtx, m.reconnectCancel = context.WithCancel(context.Background())
	m.reconnectMu.Unlock()
	m.reconnectWG.Add(1)
	go m.runReconnectLoop(m.reconnectCtx)
}

// runReconnectLoop is the background recovery driver. On a 30s
// cadence it walks every member-role session and triggers a
// reconnect on any whose Session.LastInbound() is stale (or never
// recorded). Status is no longer manipulated by this loop — Status
// is configuration state (Active/Pending/Revoked) set by the
// operator's join/leave actions and by Connect-result transitions.
// Health is computed live from LastInbound by IsSubscriberAlive.
//
// Logging levels follow the spec:
//   - debug: every reconnect attempt (success or failure)
//   - info:  every successful reconnect
//   - warn:  every backoff-interval transition
func (m *Manager) runReconnectLoop(ctx context.Context) {
	defer m.reconnectWG.Done()
	t := time.NewTicker(reconnectInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.detectStaleAndReconnect(ctx)
	}
}

// detectStaleAndReconnect walks every session (both owner-role AND
// member-role) and triggers a reconnect attempt on any whose
// Session.LastInbound() is older than subscriberStaleThreshold (or
// whose session has never observed an inbound, indicated by a zero
// LastInbound).
//
// This is the recovery driver for the unified liveness signal:
// IsSubscriberAlive is a pure function of LastInbound, and this
// pass is what wakes a stuck subscriber back up. Unlike the
// pre-#unified-liveness version, this does NOT touch
// Record.Status — Status now reflects configuration state only
// (joined/left/revoked), not subscriber health. Health is
// computed live from LastInbound on every /status read.
//
// Pre-#2580 only member-role sessions had subscribers needing
// reconnect (owner just published, didn't subscribe). Post-#2580
// (federated send) every session — owner included — has a
// peerSubs map of per-PK subscribers to other members' feeds, and
// those need the same auto-reconnect coverage. Without it, when a
// peer restarts its publisher (e.g. operator does group leave +
// rejoin to refresh a stale subscriber), the owner's peerSub to
// that peer goes stale and never recovers until the owner halts
// its own visor.
//
// Skips: revoked records, sessions without a corresponding live
// Session in m.sessions. A zero LastInbound on a live session IS
// treated as stale — we never saw any peer leaf arrive, so a
// reconnect is warranted.
//
// Granularity: staleness is checked PER-PEER. Session.PeerPKs() is
// the canonical list of peerSubs; for each, Session.PeerLastInbound()
// returns the per-peer liveness signal. Any peer whose individual
// peerSub is stale triggers a reconnect attempt, independent of how
// active the other peers are. This closes the gap where one chatty
// peer's bumps to the session-wide LastInbound() masked a silent
// peer's failed peerSub from the reconnect machinery.
//
// Reconnect path: per-stale-peer, ReconnectPeer(pk) runs one peerSub
// Connect under reconnectAttemptTimeout. The session-level
// reconnectShouldAttempt backoff still gates the whole session (we
// don't churn on a wedged group), so a peer that fails repeatedly
// inherits the session's backoff schedule rather than getting its
// own. Per-peer backoff would be a follow-up refinement if we see
// e.g. one bad peer perpetually triggering retries.
//
// Fallback: if a session has no peerSubs (legacy owner-only members
// where federated send hasn't kicked in, or owner sessions on
// pre-federated binaries), fall back to the session-wide
// LastInbound + Session.Connect path so those visors keep the
// reconnect coverage they had pre-this-change.
func (m *Manager) detectStaleAndReconnect(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	records, err := m.store.List()
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, r := range records {
		// Both Active and Pending records get the stale-check.
		// Active records that drift stale + Pending records that
		// never finished the initial Connect both want a kick from
		// the same reconnect machinery. Terminal states (Left,
		// Revoked) are skipped — the operator's intent is "not in
		// the group anymore", not "in the group but unhealthy".
		if r.Status != StatusActive && r.Status != StatusPending {
			continue
		}
		m.mu.RLock()
		sess := m.sessions[r.ID]
		m.mu.RUnlock()
		if sess == nil {
			continue
		}
		peers := sess.PeerPKs()
		if len(peers) == 0 {
			// Fallback to session-wide signal when this session
			// doesn't have peerSubs (legacy code paths). Same
			// semantics as before this change.
			last := sess.LastInbound()
			var lag time.Duration
			var reason string
			if last.IsZero() {
				lag = subscriberStaleThreshold + time.Second
				reason = "no inbound seen"
			} else {
				lag = now.Sub(last)
				reason = "last_inbound lag"
			}
			if lag <= subscriberStaleThreshold {
				continue
			}
			m.log.WithField("id", r.ID).
				WithField("lag", lag.Round(time.Second).String()).
				WithField("reason", reason).
				WithField("status", string(r.Status)).
				Debug("group: session stale; kicking reconnect (legacy session-wide path)")
			if !m.reconnectShouldAttempt(r.ID, now) {
				continue
			}
			m.tryReconnect(ctx, sess, r.ID)
			continue
		}
		// Per-peer staleness check. Collect the stale peers under
		// one read of the clock so the iteration sees a consistent
		// "now". One reconnect attempt per stale peer per tick.
		var stalePeers []cipher.PubKey
		for _, pk := range peers {
			last := sess.PeerLastInbound(pk)
			var lag time.Duration
			if last.IsZero() {
				lag = subscriberStaleThreshold + time.Second
			} else {
				lag = now.Sub(last)
			}
			if lag > subscriberStaleThreshold {
				stalePeers = append(stalePeers, pk)
			}
		}
		// Legacy s.sub staleness check — separate field, separate
		// liveness signal. Member sessions where openMember attached
		// the legacy owner-feed subscriber can have every peerSub
		// alive (so the per-peer loop above produces an empty
		// stalePeers list) while s.sub is silently wedged. The
		// per-peerSub liveness map from #2606 doesn't cover s.sub
		// because it lives outside peerSubs (deferred D4 cleanup),
		// so without this branch a wedged s.sub stays dead until the
		// operator does leave + rejoin.
		legacySubStale := false
		if sess.HasLegacySub() {
			last := sess.LegacySubLastInbound()
			var lag time.Duration
			if last.IsZero() {
				lag = subscriberStaleThreshold + time.Second
			} else {
				lag = now.Sub(last)
			}
			legacySubStale = lag > subscriberStaleThreshold
		}
		if len(stalePeers) == 0 && !legacySubStale {
			continue
		}
		// Per-peer path: tryReconnectPeers consults peerReconnectShouldAttempt
		// internally for each peer individually, so the per-peer
		// branch deliberately bypasses the session-level
		// reconnectShouldAttempt gate. Pre-T2b that gate sat HERE
		// and a single wedged peer's session-level backoff would
		// suppress every peer's reconnect attempts for the entire
		// 5min/30min window.
		if len(stalePeers) > 0 {
			m.log.WithField("id", r.ID).
				WithField("stale_peers", len(stalePeers)).
				WithField("total_peers", len(peers)).
				WithField("status", string(r.Status)).
				Debug("group: per-peer stale; kicking per-peer reconnects")
			m.tryReconnectPeers(ctx, sess, r.ID, stalePeers)
		}
		// Legacy s.sub path: still gated by the session-level
		// reconnectShouldAttempt, since s.sub failures feed into
		// reconnectRecordFailure(id, ...) which is the session-level
		// backoff. Per-peer state lives in a separate map.
		if legacySubStale {
			if !m.reconnectShouldAttempt(r.ID, now) {
				continue
			}
			m.log.WithField("id", r.ID).
				WithField("status", string(r.Status)).
				Debug("group: legacy s.sub stale; kicking owner-feed reconnect")
			m.tryReconnectLegacySub(ctx, sess, r.ID)
		}
	}
}

// tryReconnect runs one Connect attempt under reconnectAttemptTimeout.
// On success: clears per-group failure state and promotes Status →
// StatusActive (handles the join-Pending → join-Active transition;
// idempotent on records that were already Active). On failure: bumps
// the counter and applies any configured backoff transition.
// Health-reflection from this success now flows automatically:
// Session.Connect bumps lastInboundNs, so IsSubscriberAlive becomes
// true on the very next /status read.
func (m *Manager) tryReconnect(ctx context.Context, sess *Session, id string) {
	m.log.WithField("id", id).Debug("group: reconnect: attempting subscribe")
	// reconnectAttemptTimeout is the per-attempt deadline. Now
	// threaded as a context all the way through Session.Connect →
	// per-peer ps.Connect-via-goroutine, so a timeout returns the
	// caller cleanly instead of orphaning a goroutine and leaving a
	// per-peer Subscriber.Connect blocked indefinitely.
	timeoutCtx, cancel := context.WithTimeout(ctx, reconnectAttemptTimeout)
	defer cancel()
	err := sess.Connect(timeoutCtx)
	if err == nil {
		m.reconnectMu.Lock()
		delete(m.reconnectState, id)
		m.reconnectMu.Unlock()
		if sErr := m.store.SetStatus(id, StatusActive); sErr != nil {
			m.log.WithError(sErr).WithField("id", id).
				Warn("group: reconnect: SetStatus active failed")
		}
		m.log.WithField("id", id).Info("group: reconnect: subscribe restored")
		return
	}
	m.reconnectRecordFailure(id, err)
}

// tryReconnectPeers runs one ReconnectPeer attempt per stale peer
// in sequence. Each attempt is bounded by reconnectAttemptTimeout so
// a single wedged peer can't block the others. Treats per-peer
// success and failure independently: clears session-wide failure
// state only when at least one peer reconnected successfully,
// because that's positive evidence the session as a whole is
// recovering; records a failure otherwise so the per-group backoff
// schedule engages and we don't churn on a wedged group.
//
// Why sequential rather than parallel: each peer Connect can pull a
// CXO node mutex (#2599 fixed the publisher-side mutex; the
// subscriber side still serializes per node), and parallel attempts
// would just queue up behind each other anyway. Sequential keeps
// the log output readable and the worst-case wall time at
// N × reconnectAttemptTimeout, which is acceptable for typical
// group sizes (≤ 10 members).
func (m *Manager) tryReconnectPeers(ctx context.Context, sess *Session, id string, peers []cipher.PubKey) {
	now := time.Now().UTC()
	successes := 0
	skipped := 0
	for _, pk := range peers {
		if ctx.Err() != nil {
			return
		}
		// Per-peer backoff gate: a single wedged peer in 5min/30min
		// backoff doesn't lock out the other stale peers' attempts.
		// Pre-T2b this gate lived at the session level above the
		// whole tryReconnectPeers call, so any one peer accumulating
		// reconnectBackoffFailures1 failures suppressed reconnects
		// for every peer in the session for the entire backoff
		// window. Per-peer state fixes that.
		if !m.peerReconnectShouldAttempt(id, pk, now) {
			skipped++
			m.log.WithField("id", id).WithField("peer", pk.String()).
				Debug("group: reconnect: peer in backoff window, skipping this tick")
			continue
		}
		m.log.WithField("id", id).WithField("peer", pk.String()).
			Debug("group: reconnect: attempting peer subscribe")
		// Per-peer deadline threaded as a context — Session.ReconnectPeer
		// honors it via the same goroutine+select pattern as
		// Session.Connect. Cancelable, no orphan goroutine pile-up.
		timeoutCtx, cancel := context.WithTimeout(ctx, reconnectAttemptTimeout)
		err := sess.ReconnectPeer(timeoutCtx, pk)
		cancel()
		if err == nil {
			successes++
			m.peerReconnectClear(id, pk)
			m.log.WithField("id", id).WithField("peer", pk.String()).
				Info("group: reconnect: peer subscribe restored")
			continue
		}
		m.peerReconnectRecordFailure(id, pk, err)
	}
	// Session-level state still tracks "at least one peer reconnect
	// succeeded this tick" so the SetStatus(Active) promotion fires
	// when the session as a whole is recovering. We clear the
	// session-level reconnectState entry on any success to keep
	// parity with the legacy session-level backoff path used by
	// tryReconnect (full Connect) — that entry continues to gate
	// THAT path even if all per-peer attempts are skipped.
	if successes > 0 {
		m.reconnectMu.Lock()
		delete(m.reconnectState, id)
		m.reconnectMu.Unlock()
		if sErr := m.store.SetStatus(id, StatusActive); sErr != nil {
			m.log.WithError(sErr).WithField("id", id).
				Warn("group: reconnect: SetStatus active failed")
		}
	}
	// No session-level reconnectRecordFailure on the per-peer path:
	// per-peer failures are recorded against their own entries
	// inside the loop. The session-level backoff schedule is reserved
	// for tryReconnect (full Connect failure) and tryReconnectLegacySub.
	_ = skipped // tracked in log lines, no further bookkeeping
}

// tryReconnectLegacySub runs one ReconnectLegacySub attempt for the
// session's legacy s.sub owner-feed subscriber. Counterpart to
// tryReconnectPeers but for the single legacy subscriber that lives
// outside the peerSubs map (deferred D4 cleanup). No-op on owner
// sessions and on member sessions where s.sub is nil.
//
// On success: clears per-group failure state and promotes Status →
// StatusActive (same shape as tryReconnect / tryReconnectPeers).
// Session.ReconnectLegacySub bumps subLastInboundNs internally so the
// next detectStaleAndReconnect tick correctly sees the session as
// fresh. On failure: records via reconnectRecordFailure so the
// per-group backoff engages.
//
// Deliberately separate from tryReconnectPeers — they target
// different subscribers (s.sub vs. peerSubs[ownerPK]) which can
// independently go stale, and routing through a single helper would
// blur which one a given log line refers to.
func (m *Manager) tryReconnectLegacySub(ctx context.Context, sess *Session, id string) {
	if !sess.HasLegacySub() {
		return
	}
	m.log.WithField("id", id).
		Debug("group: reconnect: attempting legacy owner-feed subscribe")
	timeoutCtx, cancel := context.WithTimeout(ctx, reconnectAttemptTimeout)
	defer cancel()
	if err := sess.ReconnectLegacySub(timeoutCtx); err != nil {
		m.log.WithError(err).WithField("id", id).
			Debug("group: reconnect: legacy owner-feed subscribe failed")
		m.reconnectRecordFailure(id, err)
		return
	}
	m.reconnectMu.Lock()
	delete(m.reconnectState, id)
	m.reconnectMu.Unlock()
	if sErr := m.store.SetStatus(id, StatusActive); sErr != nil {
		m.log.WithError(sErr).WithField("id", id).
			Warn("group: reconnect: SetStatus active failed")
	}
	m.log.WithField("id", id).
		Info("group: reconnect: legacy owner-feed subscribe restored")
}

// reconnectShouldAttempt returns false when the per-group nextAttempt
// is still in the future (i.e. we're in a backoff window). True when
// there's no state (first attempt) or when the backoff has elapsed.
//
// Gates tryReconnect (full Session.Connect) and tryReconnectLegacySub;
// the per-peer path (tryReconnectPeers) uses peerReconnectShouldAttempt
// instead so a wedged session-wide backoff doesn't lock out healthy
// peers' independent reconnect attempts.
func (m *Manager) reconnectShouldAttempt(id string, now time.Time) bool {
	m.reconnectMu.Lock()
	defer m.reconnectMu.Unlock()
	st, ok := m.reconnectState[id]
	if !ok {
		return true
	}
	return !st.nextAttempt.After(now)
}

// peerReconnectShouldAttempt is the per-peer counterpart to
// reconnectShouldAttempt. Returns true when (id, peer) has no
// recorded failures or when the per-peer backoff window has elapsed.
// Lets tryReconnectPeers skip just the wedged peer rather than the
// whole session, so a single permanently-unreachable peer cannot
// engage a session-level backoff that locks out healthy peers'
// reconnect attempts.
func (m *Manager) peerReconnectShouldAttempt(id string, pk cipher.PubKey, now time.Time) bool {
	m.reconnectMu.Lock()
	defer m.reconnectMu.Unlock()
	peers, ok := m.peerReconnectState[id]
	if !ok {
		return true
	}
	st, ok := peers[pk]
	if !ok {
		return true
	}
	return !st.nextAttempt.After(now)
}

// reconnectRecordFailure increments the per-group counter and emits
// a warn-level transition log when crossing one of the configured
// failure thresholds. The backoff intervals are absolute next-attempt
// times so the reconnect loop's cheap RLock-and-skip path doesn't
// have to do any arithmetic per tick.
func (m *Manager) reconnectRecordFailure(id string, attemptErr error) {
	m.reconnectMu.Lock()
	st, ok := m.reconnectState[id]
	if !ok {
		st = &reconnectState{}
		m.reconnectState[id] = st
	}
	st.failures++
	transition := applyBackoffTransition(st)
	failures := st.failures
	m.reconnectMu.Unlock()
	if transition != "" {
		m.log.WithError(attemptErr).WithField("id", id).
			WithField("failures", failures).
			WithField("next_interval", transition).
			Warn("group: reconnect: extending backoff")
	} else {
		m.log.WithError(attemptErr).WithField("id", id).
			WithField("failures", failures).
			Debug("group: reconnect: attempt failed")
	}
}

// peerReconnectRecordFailure is the per-peer counterpart to
// reconnectRecordFailure. Increments the (id, peer) counter and
// applies the same backoff threshold transitions independently of
// any other peer's failure history. Lets one wedged peer accumulate
// its own backoff schedule (5min, 30min) without dragging the rest
// of the session into the same schedule.
func (m *Manager) peerReconnectRecordFailure(id string, pk cipher.PubKey, attemptErr error) {
	m.reconnectMu.Lock()
	peers, ok := m.peerReconnectState[id]
	if !ok {
		peers = make(map[cipher.PubKey]*reconnectState)
		m.peerReconnectState[id] = peers
	}
	st, ok := peers[pk]
	if !ok {
		st = &reconnectState{}
		peers[pk] = st
	}
	st.failures++
	transition := applyBackoffTransition(st)
	failures := st.failures
	m.reconnectMu.Unlock()
	if transition != "" {
		m.log.WithError(attemptErr).WithField("id", id).
			WithField("peer", pk.String()).
			WithField("failures", failures).
			WithField("next_interval", transition).
			Warn("group: reconnect: extending per-peer backoff")
	} else {
		m.log.WithError(attemptErr).WithField("id", id).
			WithField("peer", pk.String()).
			WithField("failures", failures).
			Debug("group: reconnect: per-peer attempt failed")
	}
}

// peerReconnectClear drops the per-peer backoff state for (id, peer).
// Called on per-peer reconnect success and on peer eviction
// (SetAllowlist removing a member) so the entry doesn't outlive its
// peerSub.
func (m *Manager) peerReconnectClear(id string, pk cipher.PubKey) {
	m.reconnectMu.Lock()
	defer m.reconnectMu.Unlock()
	peers, ok := m.peerReconnectState[id]
	if !ok {
		return
	}
	delete(peers, pk)
	if len(peers) == 0 {
		delete(m.peerReconnectState, id)
	}
}

// applyBackoffTransition extends st.nextAttempt according to the same
// failure-count thresholds reconnectRecordFailure and
// peerReconnectRecordFailure both use. Caller holds reconnectMu.
// Returns the human-readable transition interval if a threshold was
// crossed in this call, or "" otherwise. Extracted so the per-group
// and per-peer paths share one backoff schedule.
func applyBackoffTransition(st *reconnectState) string {
	switch {
	case st.failures == reconnectBackoffFailures2:
		st.nextAttempt = time.Now().UTC().Add(reconnectBackoffInterval2)
		return reconnectBackoffInterval2.String()
	case st.failures == reconnectBackoffFailures1:
		st.nextAttempt = time.Now().UTC().Add(reconnectBackoffInterval1)
		return reconnectBackoffInterval1.String()
	case st.failures > reconnectBackoffFailures2:
		// Stay at the 30min cadence for any subsequent failures.
		st.nextAttempt = time.Now().UTC().Add(reconnectBackoffInterval2)
	case st.failures > reconnectBackoffFailures1:
		// Stay at the 5min cadence between the two thresholds.
		st.nextAttempt = time.Now().UTC().Add(reconnectBackoffInterval1)
	}
	return ""
}

// kickReconnect is the opportunistic-retry hook: when a member-side
// SendToGroup succeeds via the relay listener, that proves the owner
// is reachable — so it's likely a good moment to retry the failed
// subscriber Connect too. Called outside of any session lock to avoid
// contending with the regular reconnect tick.
//
// Distinct from runReconnectLoop's 30s cadence: this fires
// immediately on a successful send, often before the next tick would,
// shortening the typical recovery from <30s to <1s when the failure
// was a transient transport hiccup.
func (m *Manager) kickReconnect(ctx context.Context, id string) {
	m.mu.RLock()
	sess, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok || sess == nil || sess.sub == nil {
		return
	}
	if sess.IsSubscriberAlive() {
		return
	}
	// Best-effort: don't block the SendToGroup caller. The retry
	// runs in its own goroutine and surfaces results through the
	// usual reconnect logs.
	go m.tryReconnect(ctx, sess, id)
}

// replaySessionHistory pumps the last resumeReplayMessageCap messages
// from a session's persistent tree through the registered handler.
// Best-effort: errors decoding individual leaves or running the
// handler are swallowed (debug-logged); a partial replay is better
// than no replay.
//
// D1 source set: every Session walks its local publisher (own
// sends + heartbeats), the legacy owner-feed subscriber (member
// sessions only), and every per-PK peer subscriber. The combined
// leaves are decoded, sorted by Message.TS, and tail-capped to
// resumeReplayMessageCap by Session.ReplayHistoryThrough.
func (m *Manager) replaySessionHistory(id string) {
	m.mu.RLock()
	sess := m.sessions[id]
	m.mu.RUnlock()
	m.onMessageMu.Lock()
	handler := m.onMessage
	m.onMessageMu.Unlock()
	if sess == nil || handler == nil {
		return
	}
	sess.ReplayHistoryThrough(handler, resumeReplayMessageCap)
}

// Close tears down every live session and returns the first error.
// Does NOT close the Store — caller still owns that.
//
// Also stops the background reconnect loop (if it was started via
// Resume). Idempotent: a second Close is a no-op.
func (m *Manager) Close() error {
	m.reconnectMu.Lock()
	cancel := m.reconnectCancel
	m.reconnectCancel = nil
	m.reconnectCtx = nil
	m.reconnectMu.Unlock()
	if cancel != nil {
		cancel()
		m.reconnectWG.Wait()
	}
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()
	var firstErr error
	for _, s := range sessions {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Get returns the persisted record for an ID.
func (m *Manager) Get(id string) (Record, bool, error) { return m.store.Get(id) }

// MarkMessageDelivered records that a message with the given timestamp
// reached the inbox for the named group. Belt-and-suspenders to keep
// LastMessageAt fresh independent of the wrapped-handler chain in
// openLocked: any subsystem with a Manager reference can call this
// after delivering a message externally (e.g. the visor's groupInbox
// when it drains a SSE push) so the persisted last_message_at stays
// consistent with what's actually flowing into the inbox.
//
// Idempotent and best-effort: errors are swallowed at debug level
// because a failure here is purely cosmetic — the message still
// reached its consumer; only the operator-visible lag indicator is
// affected.
func (m *Manager) MarkMessageDelivered(groupID string, ts time.Time) {
	if err := m.store.MarkMessage(groupID, ts); err != nil {
		m.log.WithError(err).WithField("id", groupID).
			Debug("group: MarkMessageDelivered: store update failed")
	}
	// Post-#unified-liveness: in-memory liveness now flows through
	// Session.lastInboundNs (set in Session.onUpdate on every event
	// batch). MarkMessageDelivered no longer touches a separate
	// flag; the message arrived via onUpdate before the inbox got
	// it, so lastInboundNs is already fresh by the time we land here.
	// The only side effect of this method now is the LastMessageAt
	// store-write above — the persisted projection of the in-memory
	// signal, used to seed lastInboundNs after a visor restart.
}

// IsSubscriberAlive reports the live subscriber health for the group
// id, surfaced by the chat-app's /status endpoint as the per-group
// `subscriber_alive` field. Returns true for owner-role sessions
// (no subscriber) and for member-role sessions whose most recent
// Connect() succeeded. Returns false for member-role sessions sitting
// at StatusPending or when no live session exists for the id (e.g.
// the record was left/revoked).
func (m *Manager) IsSubscriberAlive(id string) bool {
	m.mu.RLock()
	sess, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok || sess == nil {
		return false
	}
	return sess.IsSubscriberAlive()
}

// List returns every persisted record.
func (m *Manager) List() ([]Record, error) { return m.store.List() }

// BuildInvite encodes an invite link for an admin-side group. Any
// visor that IsAdmin on the record can issue invites — the founder
// implicitly, and explicit Admins entries (set via PromoteAdmin).
// Non-admin members get an error so the legacy owner-only invariant
// is preserved on records whose Admins slice is still founder-only.
func (m *Manager) BuildInvite(id string) (string, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("group: BuildInvite: no record for %s", id)
	}
	if !r.IsAdmin(m.myPK) {
		return "", errors.New("group: BuildInvite: only admins can issue invites")
	}
	return EncodeInvite(Invite{
		ID:      r.ID,
		Name:    r.Name,
		OwnerPK: r.OwnerPK,
		Port:    r.Port,
		Mode:    r.Mode,
		AESKey:  r.AESKey,
	})
}

// openLocked is the shared "open and remember" helper. Caller does
// not need to hold m.mu — this method acquires it.
func (m *Manager) openLocked(r Record) (*Session, error) {
	m.onMessageMu.RLock()
	h := m.onMessage
	m.onMessageMu.RUnlock()
	sess, err := Open(Config{
		MyPK:              m.myPK,
		MySK:              m.mySK,
		Record:            r,
		DmsgC:             m.dmsgC,
		DataDir:           m.dataDir,
		Logger:            m.log,
		HeartbeatInterval: m.heartbeatInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("group: open %s: %w", r.ID, err)
	}
	if h != nil {
		sess.SetMessageHandler(m.wrapHandler(r.ID, h))
	}
	m.mu.Lock()
	m.sessions[r.ID] = sess
	m.mu.Unlock()
	return sess, nil
}

// wrapHandler decorates the user handler so every observed message
// (inbound from the feed AND owner self-echo of own sends) updates
// the record's LastMessageAt. Without this, last_message_at only
// reflected outbound SendToGroup calls — member-side records showed
// "0001-01-01" forever even with healthy inbound, and group list's
// LAST_MESSAGE column was a misleading sender-only counter.
func (m *Manager) wrapHandler(id string, h MessageHandler) MessageHandler {
	return func(groupID string, senderPK cipher.PubKey, msg Message) {
		_ = m.store.MarkMessage(id, msg.TS) //nolint:errcheck
		h(groupID, senderPK, msg)
	}
}

// defaultPortAlloc picks a random DMSG port in the range
// [GroupPortBase, GroupPortBase+GroupPortSpan). Mirrors pairing's
// deterministic allocator in spirit but uses randomness because a
// group has no canonical "(a, b)" pair to hash from — only one
// owner, and the same owner might run many groups. Tests override
// via portAlloc.
func defaultPortAlloc() (uint16, error) {
	const (
		base = uint16(60000)
		span = uint16(5000) // [60000, 65000), well clear of the pair range
	)
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	raw := uint16(b[0])<<8 | uint16(b[1])
	return base + raw%span, nil
}

// uniqueWithSelf de-duplicates `extras` and ensures self is included.
// Owner-side semantics: the owner is always in their own allowlist
// so resuming after an unclean shutdown doesn't accidentally lock
// the owner out (`SetAllowlist([]pk{member1, member2})` minus self).
func uniqueWithSelf(self cipher.PubKey, extras []cipher.PubKey) []cipher.PubKey {
	seen := map[cipher.PubKey]struct{}{self: {}}
	out := []cipher.PubKey{self}
	for _, pk := range extras {
		if _, dup := seen[pk]; dup {
			continue
		}
		seen[pk] = struct{}{}
		out = append(out, pk)
	}
	return out
}

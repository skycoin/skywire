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

	// reconnect loop state — see runReconnectLoop.
	reconnectCtx    context.Context
	reconnectCancel context.CancelFunc
	reconnectWG     sync.WaitGroup
	reconnectMu     sync.Mutex
	reconnectState  map[string]*reconnectState // keyed by group ID
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
}

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
		store:          cfg.Store,
		dmsgC:          cfg.DmsgC,
		myPK:           cfg.MyPK,
		mySK:           cfg.MySK,
		dataDir:        cfg.DataDir,
		log:            log,
		portAlloc:      defaultPortAlloc,
		sessions:       make(map[string]*Session),
		reconnectState: make(map[string]*reconnectState),
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
	if err := sess.Connect(); err != nil {
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

// Delete tears down an owner-side group and marks it revoked.
func (m *Manager) Delete(id string) error { return m.terminate(id, StatusRevoked) }

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
	switch sess.cfg.Record.Role {
	case RoleOwner:
		if err := sess.Send(text); err != nil {
			return err
		}
	case RoleMember:
		if err := sess.SubmitToOwner(ctx, text); err != nil {
			return err
		}
		// A successful relay submission proves the owner is reachable
		// over at least one transport. If the subscriber side of this
		// session is currently down (StatusPending after a transient
		// Connect failure), now's a good moment to retry — don't wait
		// up to 30s for the next reconnect tick.
		m.kickReconnect(ctx, id)
	default:
		return fmt.Errorf("group: SendToGroup: unknown role %q", sess.cfg.Record.Role)
	}
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
	if r.Role != RoleOwner {
		return Record{}, errors.New("group: AddMember: only owner-role can edit")
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
			if err := sess.Connect(); err != nil {
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

// subscriberStaleThreshold is the lag (now - last_message_at) above
// which an "active" member session is treated as silently stale and
// demoted to StatusPending so the existing reconnect machinery kicks
// in. Set well above any plausible owner-publish gap to avoid false
// positives on genuinely quiet groups: ~10× the reconnect interval.
//
// The trade-off is recovery latency vs. unnecessary reconnects. A
// truly stuck subscriber stays stuck up to this duration after the
// CXO conn dies. A quiet group sees an unnecessary reconnect every
// time the threshold elapses with no traffic — that's OK because
// (a) reconnect is cheap, (b) ConnectPK is idempotent so the
// underlying conn is reused when healthy, and (c) the backoff
// machinery limits churn on persistent failure.
//
// A proper fix is owner-side heartbeat publication (PR-C) so
// subscribers can distinguish "no traffic" from "no pull". This
// threshold is the bridge until that lands.
const subscriberStaleThreshold = 5 * time.Minute

// reconnectAttemptTimeout bounds the per-Connect call so the
// reconnect loop can't get stuck on a single slow group while other
// pending groups starve.
const reconnectAttemptTimeout = 5 * time.Second

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

// runReconnectLoop walks every member-side session in StatusPending
// on a 30s cadence and re-attempts Connect. Successes promote the
// record back to StatusActive and reset the per-group failure
// counter. Failures bump the failure counter and, past the configured
// thresholds, extend the next-attempt time so the loop skips that
// group until the backoff window elapses.
//
// Logging levels follow the spec:
//   - debug: every reconnect attempt (success or failure)
//   - info:  every successful reconnect (StatusPending → StatusActive)
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
		// Two-pass tick: first detect silently-stale active sessions
		// and demote them to pending; then run the existing pending-
		// reconnect pass. Order matters — the demotion has to happen
		// before the pending walk so freshly-demoted records get a
		// reconnect attempt this same tick.
		m.detectStaleActive(ctx)
		m.attemptReconnectPending(ctx)
	}
}

// detectStaleActive demotes any active member session whose persisted
// last_message_at lag exceeds subscriberStaleThreshold. The demotion
// is the only signal we have today that a "healthy-looking" subscriber
// has gone silent — Resume's SetStatus(active) and tryReconnect's
// success path both leave status pinned at active until something
// flips it back. The CXO Subscriber doesn't surface "my conn died"
// to us; until owner-side heartbeat publication lands (PR-C) the lag
// is our best proxy.
//
// Demoted records pick up the reconnect loop's normal backoff state,
// so a session that genuinely cannot reconnect doesn't churn — it
// retries on the established schedule (30s → 5min after 10 fails →
// 30min after 30 fails). The wrap-around on success clears the
// backoff and restores active.
//
// Skips: owner-role sessions (no subscriber), records without a
// last_message_at (zero value — no traffic ever seen, may be a
// brand-new join), terminal records.
func (m *Manager) detectStaleActive(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	records, err := m.store.List()
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, r := range records {
		if r.Role != RoleMember || r.Status != StatusActive {
			continue
		}
		if r.LastMessageAt.IsZero() {
			continue
		}
		if now.Sub(r.LastMessageAt) <= subscriberStaleThreshold {
			continue
		}
		m.log.WithField("id", r.ID).
			WithField("lag", now.Sub(r.LastMessageAt).Round(time.Second).String()).
			Warn("group: active session stale; demoting to pending for reconnect")
		if err := m.store.SetStatus(r.ID, StatusPending); err != nil {
			m.log.WithError(err).WithField("id", r.ID).
				Debug("group: stale-detect: SetStatus pending failed")
		}
	}
}

// attemptReconnectPending iterates all member-role sessions currently
// in StatusPending and tries each one. Single-pass: a session whose
// Connect succeeds here won't be tried again until its status is
// reset to pending (e.g. on next visor restart, or a future fault).
func (m *Manager) attemptReconnectPending(ctx context.Context) {
	records, err := m.store.List()
	if err != nil {
		m.log.WithError(err).Debug("group: reconnect: store list failed")
		return
	}
	now := time.Now().UTC()
	for _, r := range records {
		if r.Role != RoleMember || r.Status != StatusPending {
			continue
		}
		if !m.reconnectShouldAttempt(r.ID, now) {
			continue
		}
		m.mu.RLock()
		sess, ok := m.sessions[r.ID]
		m.mu.RUnlock()
		if !ok || sess == nil {
			continue
		}
		m.tryReconnect(ctx, sess, r.ID)
	}
}

// tryReconnect runs one Connect attempt under reconnectAttemptTimeout.
// On success: clears per-group failure state, flips Status →
// StatusActive. On failure: bumps the counter and applies any
// configured backoff transition.
func (m *Manager) tryReconnect(ctx context.Context, sess *Session, id string) {
	m.log.WithField("id", id).Debug("group: reconnect: attempting subscribe")
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		done <- result{err: sess.Connect()}
	}()
	var err error
	select {
	case <-ctx.Done():
		return
	case <-time.After(reconnectAttemptTimeout):
		err = fmt.Errorf("group: reconnect: timed out after %s", reconnectAttemptTimeout)
	case r := <-done:
		err = r.err
	}
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

// reconnectShouldAttempt returns false when the per-group nextAttempt
// is still in the future (i.e. we're in a backoff window). True when
// there's no state (first attempt) or when the backoff has elapsed.
func (m *Manager) reconnectShouldAttempt(id string, now time.Time) bool {
	m.reconnectMu.Lock()
	defer m.reconnectMu.Unlock()
	st, ok := m.reconnectState[id]
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
	var transition string
	switch {
	case st.failures == reconnectBackoffFailures2:
		st.nextAttempt = time.Now().UTC().Add(reconnectBackoffInterval2)
		transition = reconnectBackoffInterval2.String()
	case st.failures == reconnectBackoffFailures1:
		st.nextAttempt = time.Now().UTC().Add(reconnectBackoffInterval1)
		transition = reconnectBackoffInterval1.String()
	case st.failures > reconnectBackoffFailures2:
		// Stay at the 30min cadence for any subsequent failures.
		st.nextAttempt = time.Now().UTC().Add(reconnectBackoffInterval2)
	case st.failures > reconnectBackoffFailures1:
		// Stay at the 5min cadence between the two thresholds.
		st.nextAttempt = time.Now().UTC().Add(reconnectBackoffInterval1)
	}
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
// For owner-role sessions, the publisher's tree is the source. For
// member-role sessions, the subscriber's tree (synced from the
// owner) is the source. Both expose a Walk(prefix, fn) over leaf
// values keyed by MessagePathPrefix/<ts-nano>/<seq> — sorting by
// that path string gives us chronological order, and tail-capping
// at the cap gives us the most recent N.
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

// BuildInvite encodes an invite link for an owner-side group. Returns
// an error for member-side records (members can't invite in D1; only
// the owner controls the allowlist).
func (m *Manager) BuildInvite(id string) (string, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("group: BuildInvite: no record for %s", id)
	}
	if r.Role != RoleOwner {
		return "", errors.New("group: BuildInvite: only owners can issue invites")
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
		MyPK:    m.myPK,
		MySK:    m.mySK,
		Record:  r,
		DmsgC:   m.dmsgC,
		DataDir: m.dataDir,
		Logger:  m.log,
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

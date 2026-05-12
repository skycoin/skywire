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
		store:     cfg.Store,
		dmsgC:     cfg.DmsgC,
		myPK:      cfg.MyPK,
		mySK:      cfg.MySK,
		dataDir:   cfg.DataDir,
		log:       log,
		portAlloc: defaultPortAlloc,
		sessions:  make(map[string]*Session),
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
					Warn("group: Resume: member subscribe connect")
				// Keep the record but mark pending for now; a future
				// Reconnect step (not in v1) can retry.
				_ = m.store.SetStatus(r.ID, StatusPending) //nolint:errcheck
			}
		}
		m.replaySessionHistory(r.ID)
	}
	return nil
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
func (m *Manager) Close() error {
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
		sess.SetMessageHandler(h)
	}
	m.mu.Lock()
	m.sessions[r.ID] = sess
	m.mu.Unlock()
	return sess, nil
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

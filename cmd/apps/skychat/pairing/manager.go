// Package pairing — cmd/apps/skychat/pairing/manager.go: orchestrates
// a visor's collection of chat pairs.
//
// The Manager owns the bbolt Store and a map[cipher.PubKey]*Pair of
// live runtime state. It glues persistence to lifecycle: Add records
// a pair and brings up its Pair, Resume walks the store on startup
// and reopens every active pair, Remove transitions a record to
// revoked and tears down its Pair.
//
// The pairing handshake itself (sending invite messages over legacy
// skychat, awaiting ack, transitioning pending→active) is intentionally
// out of scope — it lives one layer up in PR-3, where it can drive
// the Manager via its public surface.
package pairing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// resumeConnectTimeout bounds each Pair.Connect dial during Resume.
// Picked to match pkg/visor's pairConnectTimeout so the resume path
// has the same per-peer dial budget the operator-initiated PairAdd
// path uses; deviating here would surprise debug sessions that
// expect identical behavior across the two entry points.
const resumeConnectTimeout = 15 * time.Second

// Manager owns the persistent record set and live Pair instances.
// Safe for concurrent use.
type Manager struct {
	store   *Store
	dmsgC   *dmsg.Client
	myPK    cipher.PubKey
	mySK    cipher.SecKey
	dataDir string
	log     *logging.Logger

	// onMessage is the user-supplied callback for inbound messages.
	// Set via SetMessageHandler; applied to every Pair created or
	// resumed by the manager.
	onMessageMu sync.RWMutex
	onMessage   MessageHandler

	mu    sync.RWMutex
	pairs map[cipher.PubKey]*Pair
}

// ManagerConfig wires a Manager.
type ManagerConfig struct {
	// Store is required. The manager does not take ownership — call
	// Store.Close yourself when shutting down.
	Store *Store

	// DmsgC is the shared DMSG client passed to every Pair.
	DmsgC *dmsg.Client

	// MyPK / MySK are this visor's identity (the publisher feed-PK
	// for every pair this manager creates).
	MyPK cipher.PubKey
	MySK cipher.SecKey

	// DataDir is the parent directory under which each Pair gets
	// pub/<peer>/ and sub/<peer>/ subtrees.
	DataDir string

	// Logger is optional.
	Logger *logging.Logger
}

// NewManager constructs a manager. Does not open any pairs; call
// Resume to bring up everything in the store.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.Store == nil {
		return nil, errors.New("pairing: NewManager: Store required")
	}
	if cfg.DmsgC == nil {
		return nil, errors.New("pairing: NewManager: DmsgC required")
	}
	if cfg.DataDir == "" {
		return nil, errors.New("pairing: NewManager: DataDir required")
	}
	log := cfg.Logger
	if log == nil {
		log = logging.MustGetLogger("skychat-pair-manager")
	}
	return &Manager{
		store:   cfg.Store,
		dmsgC:   cfg.DmsgC,
		myPK:    cfg.MyPK,
		mySK:    cfg.MySK,
		dataDir: cfg.DataDir,
		log:     log,
		pairs:   make(map[cipher.PubKey]*Pair),
	}, nil
}

// SetMessageHandler installs the callback that fires for every
// inbound peer message across every pair. Replaces any prior handler
// and is propagated to existing live pairs.
func (m *Manager) SetMessageHandler(h MessageHandler) {
	m.onMessageMu.Lock()
	m.onMessage = h
	m.onMessageMu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.pairs {
		p.SetMessageHandler(h)
	}
}

// Add creates a new pending pair, persists its record, brings up the
// Pair (publisher only — Connect is the caller's choice), and
// returns it. Returns the existing Pair if one is already live for
// peerPK.
func (m *Manager) Add(peerPK cipher.PubKey) (*Pair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p, ok := m.pairs[peerPK]; ok {
		return p, nil
	}

	port, err := ComputePairPort(m.myPK, peerPK)
	if err != nil {
		return nil, fmt.Errorf("pairing: Add: %w", err)
	}

	rec := Record{
		PeerPK:        peerPK,
		Port:          port,
		Status:        StatusPending,
		EstablishedAt: time.Now().UTC(),
	}
	if err := m.store.Put(rec); err != nil {
		return nil, fmt.Errorf("pairing: Add: persist: %w", err)
	}

	p, err := m.openPair(peerPK)
	if err != nil {
		// Roll back the persisted record so a retry isn't blocked
		// by a stale entry. Best-effort: a delete failure here
		// would only mean the next start sees a stale record and
		// can heal via Resume.
		_ = m.store.Delete(peerPK) //nolint:errcheck
		return nil, err
	}
	m.pairs[peerPK] = p
	return p, nil
}

// Get returns the live Pair for peerPK, or nil + false if none.
func (m *Manager) Get(peerPK cipher.PubKey) (*Pair, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.pairs[peerPK]
	return p, ok
}

// MarkActive transitions the record for peerPK from pending to
// active. Called by the pairing handshake (PR-3) when the peer's
// pair-ack arrives.
func (m *Manager) MarkActive(peerPK cipher.PubKey) error {
	return m.store.SetStatus(peerPK, StatusActive)
}

// Remove tears down the pair, transitions its record to revoked, and
// drops it from the live map. The record is kept (rather than
// deleted) so the user can audit prior contacts; future tooling may
// expose a hard-purge.
func (m *Manager) Remove(peerPK cipher.PubKey) error {
	m.mu.Lock()
	p := m.pairs[peerPK]
	delete(m.pairs, peerPK)
	m.mu.Unlock()

	var firstErr error
	if p != nil {
		if err := p.Close(); err != nil {
			firstErr = fmt.Errorf("pairing: Remove: close pair: %w", err)
		}
	}
	if err := m.store.SetStatus(peerPK, StatusRevoked); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("pairing: Remove: set status: %w", err)
	}
	return firstErr
}

// Resume brings up every active record from the store. Skips revoked
// records. Pending records are also resumed (so a crash between
// invite-send and ack doesn't lose state); the caller can decide
// whether to re-send the invite.
//
// Errors on any single pair are logged and skipped — a corrupted or
// transient-unavailable pair shouldn't block the rest from coming
// up. The manager tracks how many resumed successfully.
func (m *Manager) Resume() (resumed int, err error) {
	records, err := m.store.List()
	if err != nil {
		return 0, fmt.Errorf("pairing: Resume: list: %w", err)
	}
	m.mu.Lock()
	// Collect resumed pairs so we can Connect them after releasing
	// m.mu. Holding the lock across Connect would serialize dmsg
	// dials behind it and turn a flaky peer into a startup stall;
	// release first and dial concurrently per pair.
	var toConnect []*Pair
	for _, rec := range records {
		if rec.Status == StatusRevoked {
			continue
		}
		if _, ok := m.pairs[rec.PeerPK]; ok {
			continue
		}
		p, openErr := m.openPair(rec.PeerPK)
		if openErr != nil {
			m.log.WithError(openErr).
				WithField("peer", rec.PeerPK.Hex()).
				Warn("pairing: Resume: skip pair")
			continue
		}
		m.pairs[rec.PeerPK] = p
		resumed++
		toConnect = append(toConnect, p)
	}
	m.mu.Unlock()

	// Connect each resumed pair's subscriber to the peer's publisher.
	// WITHOUT this, the subscriber sits idle — its OnUpdate callback
	// never fires for peer-published messages, so the visor's pair
	// inbox stays empty and PairPoll returns nothing. Pre-fix, the
	// only working path was PairAdd (initial pairing) which already
	// calls Connect; every subsequent visor restart silently broke
	// inbound pair messages until the operator re-added the pair.
	//
	// Done off the main resume path so a slow / unreachable peer
	// doesn't block visor startup — Connect failures are non-fatal
	// (same as PairAdd's best-effort dial); the pair record stays
	// in m.pairs and the next manual retry or peer reachability will
	// pick it up via a fresh PairAdd-style flow at the operator
	// layer. Per-dial bound by resumeConnectTimeout.
	for _, p := range toConnect {
		go func(p *Pair) {
			ctx, cancel := context.WithTimeout(context.Background(), resumeConnectTimeout)
			defer cancel()
			if cerr := p.Connect(ctx); cerr != nil {
				m.log.WithError(cerr).
					WithField("peer", p.cfg.PeerPK.Hex()).
					Debug("pairing: Resume: subscriber Connect failed; pair record kept")
			}
		}(p)
	}
	return resumed, nil
}

// Close tears down every live pair. Does not close the store —
// callers manage the store lifetime themselves.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for pk, p := range m.pairs {
		if err := p.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.pairs, pk)
	}
	return firstErr
}

// openPair builds a Pair for peerPK. Caller must hold m.mu.
func (m *Manager) openPair(peerPK cipher.PubKey) (*Pair, error) {
	p, err := Open(Config{
		MyPK:    m.myPK,
		MySK:    m.mySK,
		PeerPK:  peerPK,
		DmsgC:   m.dmsgC,
		DataDir: m.dataDir,
		Logger:  m.log,
	})
	if err != nil {
		return nil, err
	}
	m.onMessageMu.RLock()
	if h := m.onMessage; h != nil {
		p.SetMessageHandler(h)
	}
	m.onMessageMu.RUnlock()
	return p, nil
}

// Package serviceuptime — pkg/serviceuptime/recorder.go: long-running
// recorder that turns "this process is alive" into bbolt rows.
//
// Lifecycle:
//
//  1. New() opens the Store and writes the session-start row
//     immediately, so a process that crashes during config parsing
//     still leaves a trace.
//  2. Start() kicks off the keepalive goroutine, which on every tick
//     refreshes the session row's LastSeen and sets the current 5-min
//     slot bit.
//  3. Close() flushes the final keepalive write and shuts down.
//
// Lifecycle is split so callers can write the session row before any
// subsystem init (cheap), then defer Start() until after the bind /
// listener is up (avoiding ticks that would mark slots while the
// service can't actually serve).
package serviceuptime

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// DefaultKeepaliveInterval is how often the recorder refreshes
// LastSeen and sets the current slot bit. 60s gives one mark per
// minute, well below the 5-min slot resolution and inexpensive on
// bbolt (one Put per tick).
const DefaultKeepaliveInterval = 60 * time.Second

// DefaultRetention is the trailing window the recorder retains in
// bbolt. Matches the visor stats package's default so visor and
// services share retention semantics.
const DefaultRetention = 35 * 24 * time.Hour

// DefaultPruneInterval is how often the recorder runs Prune in the
// background. Once an hour is enough — retention is in days, missing
// a sweep by an hour doesn't change anything observable.
const DefaultPruneInterval = time.Hour

// Config tunes the Recorder. Zero values get the Default* constants.
type Config struct {
	// Service is the identifier written into each session row's
	// Service field — "skywire-visor", "transport-discovery", etc.
	Service string
	// Version / Commit identify the running binary. Captured at New()
	// and stamped into the session row; never refreshed mid-session
	// (a binary's version doesn't change without a restart).
	Version string
	Commit  string
	// KeepaliveInterval, Retention, PruneInterval override the
	// defaults. Zero leaves the default in place.
	KeepaliveInterval time.Duration
	Retention         time.Duration
	PruneInterval     time.Duration
	// Now overrides the wall clock for tests. Production callers leave nil.
	Now func() time.Time
}

// Recorder owns a running service's session row and slot bitmap.
type Recorder struct {
	store *Store
	cfg   Config

	mu      sync.Mutex
	session SessionRecord

	cancel  context.CancelFunc
	stopped chan struct{}
}

// New opens the store and writes the initial session row. The
// recorder is ready to read but does not start the keepalive ticker
// until Start() is called.
//
// path is the bbolt file location. If the file is missing it's
// created; if it exists with a different schema version the call
// errors out so the operator can decide whether to migrate.
func New(path string, cfg Config) (*Recorder, error) {
	store, err := OpenStore(path)
	if err != nil {
		return nil, err
	}
	now := cfg.now()
	r := &Recorder{
		store: store,
		cfg:   cfg,
		session: SessionRecord{
			StartedAt:  now,
			LastSeen:   now,
			Version:    cfg.Version,
			Commit:     cfg.Commit,
			PID:        os.Getpid(),
			InstanceID: store.InstanceID(),
			Service:    cfg.Service,
		},
	}
	if err := store.PutSession(r.session); err != nil {
		store.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("serviceuptime: write session row: %w", err)
	}
	if err := store.MarkSlot(now, SlotForTime(now)); err != nil {
		store.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("serviceuptime: mark initial slot: %w", err)
	}
	return r, nil
}

// Start launches the keepalive + prune goroutines. Idempotent: a
// second Start is a no-op.
func (r *Recorder) Start() {
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.stopped = make(chan struct{})
	r.mu.Unlock()

	keepalive := r.cfg.KeepaliveInterval
	if keepalive <= 0 {
		keepalive = DefaultKeepaliveInterval
	}
	pruneEvery := r.cfg.PruneInterval
	if pruneEvery <= 0 {
		pruneEvery = DefaultPruneInterval
	}
	retention := r.cfg.Retention
	if retention <= 0 {
		retention = DefaultRetention
	}

	go func() {
		defer close(r.stopped)
		kaT := time.NewTicker(keepalive)
		defer kaT.Stop()
		prT := time.NewTicker(pruneEvery)
		defer prT.Stop()
		for {
			select {
			case <-ctx.Done():
				// One last keepalive on the way out so the session's
				// LastSeen reflects the actual shutdown moment, not
				// the previous ticker fire.
				r.tick()
				return
			case <-kaT.C:
				r.tick()
			case <-prT.C:
				cutoff := r.cfg.now().Add(-retention)
				_, _, _ = r.store.Prune(cutoff) //nolint:errcheck
			}
		}
	}()
}

// tick refreshes the session row + sets the current slot bit. Called
// from the keepalive goroutine and once on shutdown.
func (r *Recorder) tick() {
	now := r.cfg.now()
	r.mu.Lock()
	r.session.LastSeen = now
	rec := r.session
	r.mu.Unlock()
	_ = r.store.PutSession(rec)                 //nolint:errcheck
	_ = r.store.MarkSlot(now, SlotForTime(now)) //nolint:errcheck
}

// Close stops the keepalive goroutine, performs one final tick so
// LastSeen reflects shutdown time, and closes the underlying store.
// Safe to call from a signal handler.
func (r *Recorder) Close() error {
	r.mu.Lock()
	cancel := r.cancel
	stopped := r.stopped
	r.cancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if stopped != nil {
		<-stopped
	} else {
		// Start was never called — fire one tick so the session's
		// LastSeen at least matches the close time.
		r.tick()
	}
	return r.store.Close()
}

// Store returns the underlying read/write store. Callers (HTTP
// handlers, CLI rendering) use this to query history. Writes
// outside the keepalive loop are tolerated but not expected.
func (r *Recorder) Store() *Store { return r.store }

// CurrentSession returns a copy of the in-flight session row, useful
// for /uptime/now style endpoints that want to surface the running
// binary's version without a bbolt round-trip.
func (r *Recorder) CurrentSession() SessionRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// readRandom is a thin wrapper around crypto/rand.Read so newInstanceID
// (in store.go) can fall back without dragging crypto/rand into every
// caller's mock setup. Centralized here so test builds can override
// via build tags if ever needed.
func readRandom(buf []byte) (int, error) {
	n, err := rand.Read(buf)
	if err != nil {
		return n, err
	}
	if n != len(buf) {
		return n, errors.New("short random read")
	}
	return n, nil
}

// Compile-time check that Recorder implements io.Closer.
var _ io.Closer = (*Recorder)(nil)

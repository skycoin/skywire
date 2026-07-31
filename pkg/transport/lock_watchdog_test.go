// Package transport pkg/transport/lock_watchdog_test.go c1-net-transport
package transport

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestLockProbe_DetectsHeldLock: while a writer holds tm.mx, lockProbe reports
// stuck; once released it reports free. This is the primitive the watchdog
// keys off to turn a silent transport-manager wedge into a loud log.
func TestLockProbe_DetectsHeldLock(t *testing.T) {
	tm := &Manager{}

	// Free lock: probe returns quickly, not stuck.
	if stuck, _ := tm.lockProbe(500 * time.Millisecond); stuck {
		t.Fatal("lockProbe reported a free lock as stuck")
	}

	// Hold the write lock and confirm the probe times out (stuck).
	tm.mx.Lock()
	released := make(chan struct{})
	go func() {
		<-released
		tm.mx.Unlock()
	}()
	if stuck, _ := tm.lockProbe(300 * time.Millisecond); !stuck {
		close(released)
		t.Fatal("lockProbe did not detect a held write lock")
	}

	// Release and confirm recovery.
	close(released)
	// The probe goroutine from the stuck call is parked on RLock; it drains
	// once we release. Give the scheduler a beat, then a fresh probe is free.
	time.Sleep(50 * time.Millisecond)
	if stuck, _ := tm.lockProbe(500 * time.Millisecond); stuck {
		t.Fatal("lockProbe still reports stuck after the lock was released")
	}
}

// TestWalkTransports_CallbackRunsUnlocked is the regression for the wedge:
// WalkTransports must NOT hold tm.mx while running the caller's callback, so a
// callback that touches the manager (or panics, or blocks) can't freeze it.
// The callback here acquires the write lock — which would deadlock instantly
// if WalkTransports still held the read lock.
func TestWalkTransports_CallbackRunsUnlocked(t *testing.T) {
	tm := &Manager{tps: map[uuid.UUID]*ManagedTransport{}}
	// Two entries so the snapshot loop iterates more than once.
	tm.tps[uuid.New()] = &ManagedTransport{}
	tm.tps[uuid.New()] = &ManagedTransport{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		tm.WalkTransports(func(_ *ManagedTransport) bool {
			// If WalkTransports held RLock here, this Lock would block forever.
			tm.mx.Lock()
			tm.mx.Unlock() //nolint:staticcheck
			return true
		})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WalkTransports held tm.mx during the callback (write-lock deadlock) — the wedge regression")
	}
}

// TestWalkTransports_PanicDoesNotLeakLock: a callback panic must not leave
// tm.mx read-locked. After a recovered panic, a writer must still be able to
// acquire the lock promptly.
func TestWalkTransports_PanicDoesNotLeakLock(t *testing.T) {
	tm := &Manager{tps: map[uuid.UUID]*ManagedTransport{}}
	tm.tps[uuid.New()] = &ManagedTransport{}

	func() {
		defer func() { recover() }() //nolint:errcheck // deliberately swallow the test panic
		tm.WalkTransports(func(_ *ManagedTransport) bool {
			panic("boom")
		})
	}()

	// The write lock must be acquirable — if the panic leaked an RLock this
	// probe times out.
	var wg sync.WaitGroup
	wg.Add(1)
	got := make(chan struct{})
	go func() {
		defer wg.Done()
		tm.mx.Lock()
		tm.mx.Unlock() //nolint:staticcheck
		close(got)
	}()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("a panicking WalkTransports callback leaked tm.mx (read lock never released)")
	}
	wg.Wait()
}

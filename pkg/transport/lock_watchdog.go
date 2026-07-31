// Package transport pkg/transport/lock_watchdog.go c1-net-transport
package transport

import (
	"runtime"
	"time"
)

// Lock-watchdog tunables. The probe tries to acquire tm.mx (read side) on a
// timer; if it cannot within lockStallThreshold the manager's lock is stuck,
// so the watchdog logs LOUDLY with a full goroutine dump (the holder's stack)
// and keeps warning until the lock frees. This turns the transport-manager
// wedge — one goroutine leaking tm.mx and freezing the whole management
// surface — from a SILENT hang into an actionable ERROR log, the same
// loud-not-silent principle applied to other deadlock classes in this tree.
const (
	lockWatchdogInterval = 30 * time.Second
	lockStallThreshold   = 20 * time.Second
)

// serveLockWatchdog periodically probes tm.mx and screams if it is stuck.
// Started as one of the Serve goroutines; exits on ctx cancel.
func (tm *Manager) serveLockWatchdog(done <-chan struct{}) {
	t := time.NewTicker(lockWatchdogInterval)
	defer t.Stop()
	stalledSince := time.Time{}
	for {
		select {
		case <-done:
			return
		case <-t.C:
		}
		if held, waited := tm.lockProbe(lockStallThreshold); held {
			if stalledSince.IsZero() {
				stalledSince = time.Now().Add(-waited)
			}
			tm.Logger.Errorf(
				"transport-manager lock STUCK for >%s (since ~%s): a holder of tm.mx has not released it — "+
					"the transport map is frozen and every reader/writer is blocked behind it. Dumping goroutines:\n%s",
				time.Since(stalledSince).Round(time.Second), stalledSince.Format(time.RFC3339), goroutineDump())
		} else if !stalledSince.IsZero() {
			tm.Logger.Warnf("transport-manager lock recovered after ~%s", time.Since(stalledSince).Round(time.Second))
			stalledSince = time.Time{}
		}
	}
}

// lockProbe reports whether tm.mx could NOT be read-acquired within timeout.
// It never blocks the caller past timeout: the acquire runs in a throwaway
// goroutine (which stays parked on the lock if it is genuinely stuck — one
// leaked goroutine is an acceptable price for the diagnostic, and it unparks
// and exits the moment the lock frees).
func (tm *Manager) lockProbe(timeout time.Duration) (stuck bool, waited time.Duration) {
	got := make(chan struct{})
	start := time.Now()
	go func() {
		tm.mx.RLock()
		tm.mx.RUnlock() //nolint:staticcheck // acquire+release is the probe
		close(got)
	}()
	select {
	case <-got:
		return false, time.Since(start)
	case <-time.After(timeout):
		return true, timeout
	}
}

// goroutineDump returns all current goroutine stacks (like SIGQUIT), bounded
// so a hub visor with thousands of goroutines can't produce an unbounded log
// line. The holder of the stuck lock is in here — that is the whole point.
func goroutineDump() []byte {
	buf := make([]byte, 1<<20) // 1 MiB cap
	n := runtime.Stack(buf, true)
	return buf[:n]
}

// Package treestore — pkg/cxo/treestore/subscriber_reconnect_test.go:
// tests for the reconnect watchdog (#2713). The watchdog detects a
// stalled subscription (no Root for > quietThreshold) and re-Connects
// to the stored publisher PK. The fix matters because dmsg silently
// drops sessions when a peer's visor restarts; the subscriber stays
// "live" in CXO's view but no data flows. Without the watchdog the
// only recovery is restarting the local visor (the workaround
// documented in #2713).
//
// These tests exercise the watchdog state machine in isolation, with
// small thresholds + manual lastUpdateNs writes — no DMSG, no real
// publisher. Connect itself fails fast in this setup (cxoNode.DMSG()
// returns nil → node.ErrAlreadyListen), so we measure
// reconnectAttempts rather than Connect-success.

package treestore

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/logging"
)

func newReconnectTestSubscriber(t *testing.T) *Subscriber {
	t.Helper()
	// Construct without DMSG. Connect will fail fast inside the
	// watchdog (DMSG()==nil → ErrAlreadyListen), but that's fine —
	// we're verifying the *decision* to call Connect, not Connect's
	// success.
	cfg := node.NewConfig()
	cxoNode, err := node.NewNode(cfg)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	t.Cleanup(func() { _ = cxoNode.Close() }) //nolint:errcheck

	s := &Subscriber{
		log:                logging.MustGetLogger("treestore-sub-reconnect-test"),
		cxoNode:            cxoNode,
		cache:              make(map[string][]byte),
		rootObservedSignal: make(chan struct{}),
		reconnectStop:      make(chan struct{}),
	}
	// Populate publisherPK so the watchdog has somewhere to dial.
	pk, _ := cipher.GenerateKeyPair()
	s.publisherPK.Store(&pk)
	return s
}

// waitFor polls cond every 5ms up to timeout, returning the time it
// became true (or t.Fatal on timeout). Used to keep tests fast
// without flaky sleep-based assertions.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestReconnectWatchdog_SkipsWhenNoActivityEver(t *testing.T) {
	// lastUpdateNs == 0 means we never saw a Root. The watchdog should
	// stay hands-off so a freshly-Connected subscription isn't churned
	// before its first publisher push arrives.
	s := newReconnectTestSubscriber(t)
	if got := s.lastUpdateNs.Load(); got != 0 {
		t.Fatalf("expected zero lastUpdateNs at start, got %d", got)
	}
	go s.runReconnectWatchdogWith(10*time.Millisecond, 50*time.Millisecond)
	defer close(s.reconnectStop)
	time.Sleep(100 * time.Millisecond)
	if got := s.reconnectAttempts.Load(); got != 0 {
		t.Errorf("expected 0 reconnect attempts when lastUpdate==0, got %d", got)
	}
}

func TestReconnectWatchdog_SkipsWhenRecent(t *testing.T) {
	// lastUpdateNs recent (within threshold) — feed is healthy, no
	// reconnect needed.
	s := newReconnectTestSubscriber(t)
	s.lastUpdateNs.Store(time.Now().UnixNano())
	go s.runReconnectWatchdogWith(10*time.Millisecond, 500*time.Millisecond)
	defer close(s.reconnectStop)
	time.Sleep(60 * time.Millisecond)
	if got := s.reconnectAttempts.Load(); got != 0 {
		t.Errorf("expected 0 reconnect attempts on healthy feed, got %d", got)
	}
}

func TestReconnectWatchdog_FiresWhenQuiet(t *testing.T) {
	// lastUpdateNs older than quietThreshold — watchdog should trigger
	// a Connect attempt on its next tick.
	s := newReconnectTestSubscriber(t)
	// 100 ms in the past, threshold is 30 ms → quiet.
	s.lastUpdateNs.Store(time.Now().Add(-100 * time.Millisecond).UnixNano())

	go s.runReconnectWatchdogWith(10*time.Millisecond, 30*time.Millisecond)
	defer close(s.reconnectStop)

	waitFor(t, 200*time.Millisecond, "first reconnect attempt", func() bool {
		return s.reconnectAttempts.Load() >= 1
	})
}

func TestReconnectWatchdog_RetriesEveryTickWhileStillQuiet(t *testing.T) {
	// The watchdog should keep retrying on each tick while the feed
	// remains quiet — not give up after one failed Connect. Important
	// for the #2713 scenario where the publisher may take a few seconds
	// to come back up after a visor restart.
	s := newReconnectTestSubscriber(t)
	s.lastUpdateNs.Store(time.Now().Add(-1 * time.Second).UnixNano())

	go s.runReconnectWatchdogWith(10*time.Millisecond, 30*time.Millisecond)
	defer close(s.reconnectStop)

	waitFor(t, 500*time.Millisecond, "multiple reconnect attempts", func() bool {
		return s.reconnectAttempts.Load() >= 3
	})
}

func TestReconnectWatchdog_StopsOnReconnectStopClosed(t *testing.T) {
	// Closing reconnectStop must terminate the watchdog goroutine
	// cleanly. Verifies the Close() path doesn't leak goroutines.
	s := newReconnectTestSubscriber(t)
	s.lastUpdateNs.Store(time.Now().Add(-1 * time.Second).UnixNano())

	done := make(chan struct{})
	go func() {
		s.runReconnectWatchdogWith(10*time.Millisecond, 30*time.Millisecond)
		close(done)
	}()
	// Let the watchdog fire at least once so we know the goroutine
	// is alive.
	waitFor(t, 200*time.Millisecond, "watchdog warmed up", func() bool {
		return s.reconnectAttempts.Load() >= 1
	})
	close(s.reconnectStop)
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("watchdog goroutine did not exit within 200ms of reconnectStop close")
	}
}

func TestReconnectWatchdog_StopsRetryingWhileActivityContinues(t *testing.T) {
	// "Quiet → fires; healthy → stops" oscillation contract: while
	// real activity keeps bumping lastUpdateNs faster than the quiet
	// threshold expires, the watchdog should stay hands-off. Models
	// a real recovered feed where Roots keep arriving.
	s := newReconnectTestSubscriber(t)
	s.lastUpdateNs.Store(time.Now().Add(-1 * time.Second).UnixNano())

	go s.runReconnectWatchdogWith(10*time.Millisecond, 100*time.Millisecond)
	defer close(s.reconnectStop)

	waitFor(t, 300*time.Millisecond, "initial reconnect attempts", func() bool {
		return s.reconnectAttempts.Load() >= 1
	})
	stableAt := s.reconnectAttempts.Load()

	// Simulate continued activity — bump every 20ms, well under the
	// 100ms quiet threshold. While this loop runs the watchdog should
	// never decide we're quiet.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			s.lastUpdateNs.Store(time.Now().UnixNano())
			time.Sleep(20 * time.Millisecond)
		}
	}()
	time.Sleep(300 * time.Millisecond) // ~30 ticks, ~15 bumps
	close(stop)
	<-done
	if got := s.reconnectAttempts.Load(); got != stableAt {
		t.Errorf("expected no new reconnect attempts under continuous activity: was %d, now %d", stableAt, got)
	}
}

// Sanity check that the atomics-backed fields don't cause a race
// when accessed from the watchdog goroutine + a synthetic "update"
// goroutine simulating handleRootFilled. Catches a future refactor
// that drops atomic.
func TestReconnectWatchdog_RaceCleanUnderConcurrentBumps(t *testing.T) {
	s := newReconnectTestSubscriber(t)
	s.lastUpdateNs.Store(time.Now().Add(-1 * time.Second).UnixNano())

	go s.runReconnectWatchdogWith(5*time.Millisecond, 20*time.Millisecond)
	defer close(s.reconnectStop)

	var bumps atomic.Int64
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			s.lastUpdateNs.Store(time.Now().UnixNano())
			bumps.Add(1)
			time.Sleep(time.Millisecond)
		}
	}()
	time.Sleep(100 * time.Millisecond)
	close(stop)
	// No assertion on counts — `go test -race` is the actual judge.
	// We just want bumps to have happened so the race window is open.
	if bumps.Load() == 0 {
		t.Fatal("bumper goroutine never ran")
	}
}

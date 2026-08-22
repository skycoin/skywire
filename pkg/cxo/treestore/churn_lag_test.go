// Package treestore — churn_lag_test.go: measures how fast a CXO subscriber
// (the TPD role) converges to a publisher (the visor role) that mutates its
// tree hard — the exact question behind "TPD lags a burst of transport
// add/removes". Runs over the native localhost TCP transport (no dmsg /
// discovery), so the numbers are the CXO publish→fill pipeline's own latency,
// not network. Not a pass/fail gate for CI timing (it asserts only
// completeness + a generous ceiling); its value is the logged lag figures.
package treestore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// churnSub tracks the subscriber's live view as a path→present set plus the
// wall-clock of the last event, so a test can ask "is the subscriber's set
// equal to the publisher's final set yet, and when did it get there".
type churnSub struct {
	mu       sync.Mutex
	present  map[string]struct{}
	events   int // total UpdateEvent entries observed
	roots    int // total OnUpdate callbacks (≈ Roots filled)
	lastEvAt time.Time
}

func newChurnSub() *churnSub { return &churnSub{present: map[string]struct{}{}} }

func (c *churnSub) apply(events []UpdateEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.roots++
	for _, ev := range events {
		c.events++
		if ev.Value == nil {
			delete(c.present, ev.Path)
		} else {
			c.present[ev.Path] = struct{}{}
		}
	}
	c.lastEvAt = time.Now()
}

func (c *churnSub) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.present)
}

func (c *churnSub) snapshot() (int, int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.present), c.events, c.roots
}

// waitConverge blocks until the subscriber's present-set size equals want (or
// timeout), returning the time it took and whether it got there.
func (c *churnSub) waitConverge(want int, timeout time.Duration) (time.Duration, bool) {
	start := time.Now()
	end := start.Add(timeout)
	for {
		if c.size() == want {
			return time.Since(start), true
		}
		if time.Now().After(end) {
			return time.Since(start), false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func newChurnPair(t *testing.T, batchWindow time.Duration) (*Publisher, *churnSub) {
	t.Helper()
	_, skA := cipher.GenerateKeyPair()
	pkA := cipher.PubKey{}
	{
		// derive pkA from skA so subscriber can address the feed
		pk, err := skA.PubKey()
		require.NoError(t, err)
		pkA = pk
	}
	pub, err := NewWithTCP("127.0.0.1:0", skA, PubConfig{
		InMemoryDB:  true,
		BatchWindow: batchWindow,
	})
	require.NoError(t, err, "NewWithTCP publisher")
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	addr := pub.Node().TCP().Address()
	require.NotEmpty(t, addr)

	cs := newChurnSub()
	sub, err := NewSubscriberTCP("", pkA, SubConfig{InMemoryDB: true})
	require.NoError(t, err, "NewSubscriberTCP")
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
	sub.OnUpdate(cs.apply)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, sub.ConnectTCP(ctx, addr), "ConnectTCP")
	return pub, cs
}

// TestCXOChurn_BurstFill mirrors "43 transports appear at once": publish N
// entries as fast as the publisher accepts them, then measure how long until
// the subscriber's set holds all N. This is the convergence lag a fresh
// transport burst should incur.
func TestCXOChurn_BurstFill(t *testing.T) {
	for _, bw := range []time.Duration{5 * time.Millisecond, 100 * time.Millisecond} {
		t.Run(fmt.Sprintf("batch=%s", bw), func(t *testing.T) {
			pub, cs := newChurnPair(t, bw)
			const n = 43 // the observed live burst size

			pubStart := time.Now()
			for i := 0; i < n; i++ {
				require.NoError(t, pub.Put(fmt.Sprintf("tp/%05d", i), []byte(fmt.Sprintf("edge-%d", i))))
			}
			pubDur := time.Since(pubStart)

			lag, ok := cs.waitConverge(n, 10*time.Second)
			present, events, roots := cs.snapshot()
			t.Logf("burst N=%d batch=%s: publish took %s; subscriber converged=%v in %s (present=%d events=%d roots=%d)",
				n, bw, pubDur.Round(time.Millisecond), ok, lag.Round(time.Millisecond), present, events, roots)
			require.True(t, ok, "subscriber must converge to all %d entries", n)
		})
	}
}

// TestCXOChurn_Sustained sweeps realistic mutation rates (transport flapping is
// tens/sec at most, nowhere near in-memory loop speed) and measures the tail
// convergence lag once churn stops, WITH a final Flush so the last batch is
// published. It answers "does the subscriber end up consistent, and how fast"
// and isolates whether the extreme-rate lost-delete is a real hazard at plausible
// rates or only an artifact of an unthrottled loop.
func TestCXOChurn_Sustained(t *testing.T) {
	for _, mutPerSec := range []int{20, 200, 2000} {
		t.Run(fmt.Sprintf("rate=%d/s", mutPerSec), func(t *testing.T) {
			pub, cs := newChurnPair(t, 20*time.Millisecond)

			const (
				churnDur   = 4 * time.Second
				liveTarget = 40
			)
			for i := 0; i < liveTarget; i++ {
				require.NoError(t, pub.Put(fmt.Sprintf("tp/%05d", i), []byte("seed")))
			}
			require.NoError(t, pub.Flush())
			_, seedOK := cs.waitConverge(liveTarget, 5*time.Second)
			require.True(t, seedOK, "seed set must converge before churn")

			// One mutation = add newest + delete oldest (net-zero live size),
			// paced to mutPerSec.
			interval := time.Second / time.Duration(mutPerSec)
			next, oldest, ops := liveTarget, 0, 0
			churnEnd := time.Now().Add(churnDur)
			for time.Now().Before(churnEnd) {
				tick := time.Now()
				require.NoError(t, pub.Put(fmt.Sprintf("tp/%05d", next), []byte("live")))
				require.NoError(t, pub.Delete(fmt.Sprintf("tp/%05d", oldest)))
				next++
				oldest++
				ops += 2
				if d := interval - time.Since(tick); d > 0 {
					time.Sleep(d)
				}
			}
			// Force the final batch out — mirrors a visor's tp publisher settling
			// after the flap stops.
			require.NoError(t, pub.Flush())

			tailLag, ok := cs.waitConverge(liveTarget, 10*time.Second)
			present, events, roots := cs.snapshot()
			t.Logf("churn rate=%d/s: %d ops in %s; post-churn+flush converge=%v in %s; final present=%d (want %d) events=%d roots=%d",
				mutPerSec, ops, churnDur, ok, tailLag.Round(time.Millisecond), present, liveTarget, events, roots)
			require.True(t, ok, "subscriber must converge to the final live set after churn stops (rate=%d/s)", mutPerSec)
			require.Equal(t, liveTarget, present, "no stale/lost entries after convergence (rate=%d/s)", mutPerSec)
		})
	}
}

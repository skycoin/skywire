package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A leg whose transport fails under a write must not fail the app's write while
// another leg is up. Live, a cut squicr leg closed the exit's skysocks session
// and every stream on the tunnel with it.
func TestRouteGroupWriteSurvivesALegFailingUnderIt(t *testing.T) {
	rg, conns := createCapturingMuxRouteGroup(t)
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close() //nolint:errcheck,gosec
		}
	})
	require.NoError(t, rg.SetWriteDeadline(time.Now().Add(5*time.Second)))
	conns[0].workingTransport.Close() //nolint:errcheck,gosec

	rg.mu.Lock()
	tp, rule := rg.tps[0], rg.fwd[0]
	rg.mu.Unlock()
	n, err := rg.write([]byte("payload"), tp, rule, 0)
	require.NoError(t, err)
	require.Equal(t, len("payload"), n)
	require.Equal(t, []uint32{0}, conns[1].dataSeqs(), "the frame must go out on the surviving leg")

	n, err = rg.Write([]byte("more"))
	require.NoError(t, err)
	require.Equal(t, len("more"), n)
}

// With no other leg the failure is still the caller's to see.
func TestRouteGroupWriteFailsWhenNoOtherLegIsUp(t *testing.T) {
	rg, conns := createCapturingMuxRouteGroup(t)
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close() //nolint:errcheck,gosec
		}
	})
	require.NoError(t, rg.SetWriteDeadline(time.Now().Add(5*time.Second)))
	conns[0].workingTransport.Close() //nolint:errcheck,gosec
	conns[1].workingTransport.Close() //nolint:errcheck,gosec

	rg.mu.Lock()
	tp, rule := rg.tps[0], rg.fwd[0]
	rg.mu.Unlock()
	_, err := rg.write([]byte("payload"), tp, rule, 0)
	require.Error(t, err)
}

// The read loop can prune the dead leg between the leg pick and the failed
// write. The frame must still go out on the one leg left. Live, the exit closed
// its session with one healthy leg remaining.
func TestRouteGroupWriteSurvivesALegPrunedBeforeItsWriteFails(t *testing.T) {
	rg, conns := createCapturingMuxRouteGroup(t)
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close() //nolint:errcheck,gosec
		}
	})
	require.NoError(t, rg.SetWriteDeadline(time.Now().Add(5*time.Second)))
	conns[0].workingTransport.Close() //nolint:errcheck,gosec

	rg.mu.Lock()
	tp, rule := rg.tps[0], rg.fwd[0]
	rg.tps, rg.fwd, rg.rvs = rg.tps[1:], rg.fwd[1:], rg.rvs[1:]
	rg.mu.Unlock()

	n, err := rg.write([]byte("payload"), tp, rule, 0)
	require.NoError(t, err)
	require.Equal(t, len("payload"), n)
	require.Equal(t, []uint32{0}, conns[1].dataSeqs(), "the frame must go out on the remaining leg")
}

package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestIntroduceRules_AcceptSendDoesNotHoldMutex is a regression test for the
// router-wide deadlock observed live on a loaded exit visor: IntroduceRules
// handed a NEW route group off to AcceptRoutes with a send to r.accept made
// WHILE HOLDING r.mx. When the accept buffer filled (a burst of route setups
// with the app-accept loop mid-handshake), that send blocked with r.mx held —
// and the only consumer, AcceptRoutes -> saveRouteGroupRules, needs r.mx to
// drain the next item. Circular wait: the router wedged, every other
// IntroduceRules / ActiveRouteStatuses / rules-GC piled up behind r.mx (~10k
// goroutines, route setup failing fleet-wide with "context deadline exceeded").
//
// The invariant this test pins: while IntroduceRules is parked on a full accept
// buffer, r.mx must remain free so the consumer can drain and unwedge it.
func TestIntroduceRules_AcceptSendDoesNotHoldMutex(t *testing.T) {
	l := logging.NewMasterLogger()
	r := &router{
		logger: l.PackageLogger("router-deadlock-test"),
		conf:   &Config{},
		rt:     routing.NewTable(l.PackageLogger("rt")),
		rgsNs:  make(map[routing.RouteDescriptor]*NoiseRouteGroup),
		rgsRaw: make(map[routing.RouteDescriptor]*RouteGroup),
		accept: make(chan routing.EdgeRules, 1),
		done:   make(chan struct{}),
	}
	// Fill the single accept slot so the next send blocks.
	r.accept <- routing.EdgeRules{}

	initPK, _ := cipher.GenerateKeyPair()
	respPK, _ := cipher.GenerateKeyPair()
	desc := routing.NewRouteDescriptor(initPK, respPK, 1, 2)
	consume := routing.ConsumeRule(time.Hour, routing.RouteID(1), respPK, initPK, 1, 2)
	rules := routing.EdgeRules{Desc: desc, Forward: consume, Reverse: consume}

	introDone := make(chan struct{})
	var introErr error
	go func() {
		introErr = r.IntroduceRules(rules)
		close(introDone)
	}()

	// Let IntroduceRules progress past its brief bookkeeping section (three map
	// lookups) and park on the blocking accept send. Microsecond-scale work, so
	// 50ms is a wide margin.
	time.Sleep(50 * time.Millisecond)

	// IntroduceRules must still be blocked on the send (buffer is full) — this is
	// the scenario under test, not a no-op.
	select {
	case <-introDone:
		t.Fatal("IntroduceRules returned before the accept buffer was drained; test scenario invalid")
	default:
	}

	// The core assertion: r.mx is acquirable while IntroduceRules is parked on
	// the full send. Before the fix this blocked forever (the deadlock).
	mxFree := make(chan struct{})
	go func() {
		r.mx.Lock()
		r.mx.Unlock() //nolint:staticcheck // just proving the lock is obtainable
		close(mxFree)
	}()
	select {
	case <-mxFree:
	case <-time.After(2 * time.Second):
		t.Fatal("r.mx held while IntroduceRules blocked on a full accept send — router deadlock regression")
	}

	// Drain the filler so IntroduceRules' send completes and the goroutine exits.
	<-r.accept
	select {
	case <-introDone:
		require.NoError(t, introErr)
	case <-time.After(2 * time.Second):
		t.Fatal("IntroduceRules did not complete after the accept buffer drained")
	}
}

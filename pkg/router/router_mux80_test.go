// Package router pkg/router/router_mux80_test.go
//
// Regression tests for #80: on the RESPONDER, an aux (2nd..N) mux leg that
// arrives (via AddEdgeRules -> IntroduceRules) while the primary route group is
// still initializing (present in rgsRaw, not yet rgsNs) used to be pushed to
// r.accept, where AcceptRoutes' second saveRouteGroupRules either DELETED the
// leg's freshly-installed rules or blocked the whole handshake-await — either
// way the leg black-holed and the mux collapsed back toward a single leg.
//
// Fix A buffers such a leg (pendingLegs) and drains it through appendRouteToGroup
// the moment the group registers; Fix B stops AcceptRoutes deleting the leg's
// rules for the duplicate-descriptor case.
package router

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// newLegTestRouter builds a minimal responder-side router with a real transport
// manager (so appendRouteToGroup can resolve injected aux-leg transports) and
// the maps IntroduceRules / saveRouteGroupRules / AcceptRoutes touch.
func newLegTestRouter(t *testing.T) *router {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	tm, err := transport.NewManager(nil, nil, nil, &transport.ManagerConfig{
		PubKey:          pk,
		SecKey:          sk,
		DiscoveryClient: transport.NewDiscoveryMock(),
	}, network.ClientFactory{})
	require.NoError(t, err)

	return &router{
		logger:        logging.MustGetLogger("mux80_test"),
		mLogger:       logging.NewMasterLogger(),
		conf:          &Config{PubKey: pk, SecKey: sk},
		tm:            tm,
		rt:            routing.NewTable(logging.MustGetLogger("mux80_rt")),
		rgsNs:         make(map[routing.RouteDescriptor]*NoiseRouteGroup),
		rgsRaw:        make(map[routing.RouteDescriptor]*RouteGroup),
		rgsDatagrams:  make(map[routing.RouteDescriptor]*DatagramRouteGroup),
		datagramPorts: make(map[routing.Port]struct{}),
		pending:       newPendingPackets(),
		pendingLegs:   newPendingLegs(),
		accept:        make(chan routing.EdgeRules, acceptSize),
		done:          make(chan struct{}),
	}
}

// legTestKeys are the (peer, local) identities the responder's descriptor is
// keyed by; a package-level pair keeps every leg for a test sharing one desc.
func legTestKeys() (peer, local cipher.PubKey) {
	peer, _ = cipher.GenerateKeyPair()
	local, _ = cipher.GenerateKeyPair()
	return peer, local
}

const (
	legTestSrcPort routing.Port = 100
	legTestDstPort routing.Port = 200
)

// setupInitializingPrimary installs a mux-enabled primary route group for a
// fresh descriptor into rgsRaw ONLY — modelling the mid-handshake window before
// the group registers into rgsNs. Returns the group and its descriptor.
func (r *router) setupInitializingPrimary(t *testing.T) (*RouteGroup, routing.RouteDescriptor) {
	t.Helper()
	peer, local := legTestKeys()
	desc := routing.NewRouteDescriptor(peer, local, legTestSrcPort, legTestDstPort)

	primaryTpID := uuid.New()
	mt := transport.NewManagedTransportForTest(newWorkingTransport())
	mt.Entry = transport.Entry{ID: primaryTpID, Type: "test"}
	r.tm.InjectTransportForTest(mt)

	fwd := routing.ForwardRule(DefaultRouteKeepAlive, routing.RouteID(1), routing.RouteID(101), primaryTpID, local, peer, legTestDstPort, legTestSrcPort)
	rvs := routing.ConsumeRule(DefaultRouteKeepAlive, routing.RouteID(101), local, peer, legTestSrcPort, legTestDstPort)
	require.NoError(t, r.rt.SaveRule(fwd))
	require.NoError(t, r.rt.SaveRule(rvs))

	rg := NewRouteGroup(DefaultRouteGroupConfig(), r.rt, desc, r.mLogger)
	rg.mux = newRouteMux(r.mLogger.PackageLogger("mux80_mux"), false)
	rg.initiator = false
	rg.appendRules(fwd, rvs, mt)

	r.mx.Lock()
	r.rgsRaw[desc] = rg
	r.mx.Unlock()

	return rg, desc
}

// makeAuxRules builds a distinct aux mux leg (unique route IDs + a freshly
// injected transport) for desc. i must be unique within a test.
func (r *router) makeAuxRules(t *testing.T, desc routing.RouteDescriptor, i int) routing.EdgeRules {
	t.Helper()
	peer := desc.SrcPK()
	local := desc.DstPK()

	auxTpID := uuid.New()
	mt := transport.NewManagedTransportForTest(newWorkingTransport())
	mt.Entry = transport.Entry{ID: auxTpID, Type: "test"}
	r.tm.InjectTransportForTest(mt)

	fwd := routing.ForwardRule(DefaultRouteKeepAlive, routing.RouteID(1000+i), routing.RouteID(2000+i), auxTpID, local, peer, legTestDstPort, legTestSrcPort)
	rvs := routing.ConsumeRule(DefaultRouteKeepAlive, routing.RouteID(2000+i), local, peer, legTestSrcPort, legTestDstPort)
	return routing.EdgeRules{Desc: desc, Forward: fwd, Reverse: rvs}
}

// registerPrimary runs the exact registration step saveRouteGroupRules performs
// once the primary's handshake completes: move rgsRaw -> rgsNs, then drain any
// aux legs buffered during the raw window.
func (r *router) registerPrimary(rg *RouteGroup, desc routing.RouteDescriptor) {
	nrg := &NoiseRouteGroup{rg: rg, Conn: rg}
	r.mx.Lock()
	r.rgsNs[desc] = nrg
	delete(r.rgsRaw, desc)
	r.mx.Unlock()
	r.drainPendingLegs(nrg, desc)
}

func legCount(rg *RouteGroup) int {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	return len(rg.tps)
}

// TestIntroduceRules_AuxLegDuringInitialization is the core Fix A regression:
// an aux leg arriving while the primary is in rgsRaw must be buffered (not
// dropped, not pushed to accept, rules not deleted) and appended once the group
// registers. This assertion set FAILS on develop (the aux leg is pushed to
// r.accept and never appended).
func TestIntroduceRules_AuxLegDuringInitialization(t *testing.T) {
	r := newLegTestRouter(t)
	rg, desc := r.setupInitializingPrimary(t)
	require.Equal(t, 1, legCount(rg), "primary starts as a single leg")

	aux := r.makeAuxRules(t, desc, 1)
	require.NoError(t, r.IntroduceRules(aux))

	// (i) the aux leg's freshly-installed rules must survive.
	_, errF := r.rt.Rule(aux.Forward.KeyRouteID())
	require.NoError(t, errF, "aux forward rule must not be deleted")
	_, errR := r.rt.Rule(aux.Reverse.KeyRouteID())
	require.NoError(t, errR, "aux reverse rule must not be deleted")

	// (ii) nothing may be pushed to r.accept (that path is what collapses mux).
	select {
	case <-r.accept:
		t.Fatal("aux leg arriving during initialization must not be pushed to r.accept")
	default:
	}

	// It is buffered, not yet appended, while the group is still initializing.
	require.Equal(t, 1, legCount(rg), "aux leg must not append until the group registers")

	// (iii) once the primary registers, the buffered aux leg is appended.
	r.registerPrimary(rg, desc)
	require.Equal(t, 2, legCount(rg), "buffered aux leg must be appended on registration")
	require.False(t, rg.isClosed(), "primary route group must not be closed")
}

// TestAcceptRoutes_DuplicateDescDoesNotDeleteLeg guards Fix B: if an aux leg for
// an already-initializing descriptor reaches AcceptRoutes, the leg's rules must
// survive (AcceptRoutes must not DelRules them). On develop AcceptRoutes deletes
// them, black-holing the leg.
func TestAcceptRoutes_DuplicateDescDoesNotDeleteLeg(t *testing.T) {
	r := newLegTestRouter(t)
	_, desc := r.setupInitializingPrimary(t)

	aux := r.makeAuxRules(t, desc, 1)
	// Drive the (pre-Fix-A) path directly: hand the aux leg to the accept loop.
	r.accept <- aux

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := r.AcceptRoutes(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, errRouteGroupInitializing)

	_, errF := r.rt.Rule(aux.Forward.KeyRouteID())
	require.NoError(t, errF, "forward rule must survive the duplicate-descriptor accept")
	_, errR := r.rt.Rule(aux.Reverse.KeyRouteID())
	require.NoError(t, errR, "reverse rule must survive the duplicate-descriptor accept")
}

// TestMuxSetup_ConcurrentLegsNoCollapse stands up N aux legs concurrently to the
// same descriptor while the primary sits in rgsRaw, with registration racing the
// arrivals after a deterministic delay (modelling the responder handshake-await
// window). The group must converge to N legs, the primary rg must never be
// closed or replaced, and its src port must be stable. Run under -race.
func TestMuxSetup_ConcurrentLegsNoCollapse(t *testing.T) {
	const nAux = 3
	r := newLegTestRouter(t)
	rg, desc := r.setupInitializingPrimary(t)
	srcPortBefore := rg.desc.SrcPort()

	auxRules := make([]routing.EdgeRules, nAux)
	for i := 0; i < nAux; i++ {
		auxRules[i] = r.makeAuxRules(t, desc, i+1)
	}

	var wg sync.WaitGroup

	// Registration races the aux arrivals.
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(15 * time.Millisecond)
		r.registerPrimary(rg, desc)
	}()

	// Aux legs arrive from parallel goroutines, straddling the registration.
	for i := 0; i < nAux; i++ {
		wg.Add(1)
		go func(rules routing.EdgeRules) {
			defer wg.Done()
			require.NoError(t, r.IntroduceRules(rules))
		}(auxRules[i])
	}
	wg.Wait()

	// Every aux leg lands exactly once (buffered+drained if it arrived during the
	// raw window, immediate-append if after registration) — no collapse.
	require.Equal(t, 1+nAux, legCount(rg), "group must converge to primary + all aux legs")
	require.False(t, rg.isClosed(), "primary route group must never be closed")

	r.mx.Lock()
	nrg, ok := r.rgsNs[desc]
	r.mx.Unlock()
	require.True(t, ok, "the group must be registered in rgsNs")
	require.Same(t, rg, nrg.rg, "the primary rg must never be replaced")
	require.Equal(t, srcPortBefore, rg.desc.SrcPort(), "the primary src port must be stable")
}

// Package router pkg/router/router_mux_ops_test.go
//
// Regression coverage for the per-leg hop-chain telemetry on RUNTIME-added mux
// legs. Every runtime add path — GrowMuxRoute / `cli proxy mux add` (both via
// AddMuxRouteByHops), the async dial-time aux legs (establishMuxRoutes), and
// the rotation / self-heal re-dials (addOneAuxLeg) — must record the forward
// route it dialed with (recordLegHops) so MuxStats reports the leg's WHOLE
// chain, not just its first-hop transport remote (which for a multihop leg is
// the first INTERMEDIATE, mislabeled as the exit). This test drives the one
// path that exercises the full runtime machinery end to end without a network:
// AddMuxRouteByHops with a stubbed setup-node dialer.
package router

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// stubLegDialer is a RouteGroupDialer that skips the setup-node round-trip and
// hands back pre-built edge rules, so the test runs the REAL runtime add-leg
// sequence (validation → dial → appendRouteToGroup → recordLegHops) in-process.
type stubLegDialer struct{ rules routing.EdgeRules }

func (d stubLegDialer) Dial(_ context.Context, _ *logging.Logger, _ *dmsg.Client, _ []cipher.PubKey, _ routing.BidirectionalRoute) (routing.EdgeRules, cipher.PubKey, error) {
	return d.rules, cipher.PubKey{}, nil
}

// TestAddMuxRouteByHopsRecordsLegHopsInMuxStats proves that a mux leg added at
// RUNTIME (not the initial dial) surfaces its full forward hop chain through
// MuxStats — the source `cli proxy mux info` (.legs[].hops), `cli proxy tree`
// and the status.skysocks route tree all read from. The keying contract under
// test: recordLegHops stores the dialed route under its first hop's TpID, and
// MuxStats resolves it via legHopsFor(tp.Entry.ID) — so the planned route's
// first hop must ride the same transport the committed rules point at.
func TestAddMuxRouteByHopsRecordsLegHopsInMuxStats(t *testing.T) {
	r := newLegTestRouter(t)

	// A live, mux-enabled route group registered in rgsNs — the state every
	// runtime add-leg caller requires before it will append.
	rg, desc := r.setupInitializingPrimary(t)
	nrg := &NoiseRouteGroup{rg: rg}
	r.mx.Lock()
	delete(r.rgsRaw, desc)
	r.rgsNs[desc] = nrg
	r.mx.Unlock()

	// The aux leg's rules + first-hop transport (makeAuxRules injects the
	// transport into the manager) — exactly what a successful setup-node
	// dial returns to AddMuxRouteByHops.
	auxRules := r.makeAuxRules(t, desc, 1)
	auxTpID := auxRules.Forward.NextTransportID()
	r.conf.RouteGroupDialer = stubLegDialer{rules: auxRules}

	// The planned 2-hop forward route: this visor → mid → peer. In the
	// group's descriptor orientation this visor is DstPK (the entry point)
	// and the far peer is SrcPK — the same orientation AddMuxRouteByHops
	// validates against.
	local := desc.DstPK()
	peer := desc.SrcPK()
	mid, _ := cipher.GenerateKeyPair()
	fwd := []routing.Hop{
		{TpID: auxTpID, From: local, To: mid},
		{TpID: uuid.New(), From: mid, To: peer},
	}
	rev := []routing.Hop{
		{TpID: uuid.New(), From: peer, To: mid},
		{TpID: auxTpID, From: mid, To: local},
	}

	require.NoError(t, r.AddMuxRouteByHops(desc, fwd, rev))

	// The aux-added leg must report EVERY hop, ending at the true
	// destination. Before hop recording was wired into the runtime add
	// paths, such a leg showed an empty hop list and only its first-hop
	// remote — the telemetry gap this file guards against regressing.
	info := rg.MuxStats()
	require.True(t, info.MuxEnabled)
	require.Len(t, info.Legs, 2, "primary + the runtime-added aux leg")

	var aux *MuxLeg
	for i := range info.Legs {
		if info.Legs[i].TransportID == auxTpID.String() {
			aux = &info.Legs[i]
		}
	}
	require.NotNil(t, aux, "the added leg must appear in MuxStats on its first-hop transport")
	require.Len(t, aux.Hops, 2, "an aux-added leg reports its whole chain, not just the first-hop remote")
	require.Equal(t, auxTpID.String(), aux.Hops[0].TpID, "hop 0 rides the leg's own transport — the legHopsFor key")
	require.Equal(t, local.String(), aux.Hops[0].From, "the chain starts at this visor")
	require.Equal(t, mid.String(), aux.Hops[0].To, "the first hop lands on the intermediate")
	require.Equal(t, peer.String(), aux.Hops[1].To, "the last hop ends at the destination, not the intermediate")
}

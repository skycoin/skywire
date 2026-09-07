// Package router pkg/router/router_mux_remote_leg_test.go
//
// Regression coverage for the setup-node churn measured on sn1
// (0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557): 80-88%
// of its route-setup failures were one error, raised by the DESTINATION visor —
// "failed to broadcast rules to destination router: failure code 1 (refusing to
// append mux leg over transport <uuid> already in the group)".
//
// Mechanism: the setup node installs `respEdge.Forward` on the destination, and
// GenerateRules keys fwdRules by each route's Hops[0].From — so the rule the
// destination receives is the REVERSE route's first hop, and the destination's
// route group registers exactly Reverse[0].TpID for that leg. Its
// appendRouteToGroup then refuses any leg whose transport is already in the
// group. Every initiator-side guard shipped so far (#4095/#4096) checks
// Forward[0].TpID against the INITIATOR's transports, which cannot see this;
// and because pickDisjointPath ranks forward and reverse INDEPENDENTLY and a
// direct (0-intermediate) reverse gives ExcludeIntermediatePKs nothing to bite
// on, the finder keeps pairing a disjoint forward with a reverse over the
// destination's primary-leg transport.
//
// These tests pin the initiator-side state that makes the collision predictable
// (RouteGroup.legRemoteTp / remoteLegTransportIDs) and the gate that stops the
// doomed plan BEFORE the setup-node dial.
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

// countingLegDialer is stubLegDialer plus a call counter: the point of the fix
// is that a doomed leg never reaches the setup node at all, so "was Dial
// called" is the assertion that matters, not just the returned error.
type countingLegDialer struct {
	rules routing.EdgeRules
	calls int
}

func (d *countingLegDialer) Dial(_ context.Context, _ *logging.Logger, _ *dmsg.Client, _ []cipher.PubKey, _ routing.BidirectionalRoute) (routing.EdgeRules, cipher.PubKey, error) {
	d.calls++
	return d.rules, cipher.PubKey{}, nil
}

// TestRemoteLegTransportIDsMirrorsDestinationLegs pins the bookkeeping the gate
// reads: remoteLegTransportIDs must report the far end's first-hop transport
// for every LIVE leg, and must stop reporting one as soon as its leg is gone —
// otherwise a pruned leg would permanently lock out a replacement over that
// same far-end link.
func TestRemoteLegTransportIDsMirrorsDestinationLegs(t *testing.T) {
	r := newLegTestRouter(t)
	rg, desc := r.setupInitializingPrimary(t)

	local := desc.DstPK()
	peer := desc.SrcPK()

	// Primary leg: 1-hop direct, so the destination installs its ForwardRule
	// over the very same direct transport (fwd[0].TpID == rev[0].TpID).
	rg.mu.Lock()
	primaryTpID := rg.tps[0].Entry.ID
	rg.mu.Unlock()
	rg.recordLegRoute(
		[]routing.Hop{{TpID: primaryTpID, From: local, To: peer}},
		[]routing.Hop{{TpID: primaryTpID, From: peer, To: local}},
	)

	require.Equal(t, []uuid.UUID{primaryTpID}, rg.remoteLegTransportIDs(),
		"the destination holds this leg on the direct transport; that is what a second leg must avoid")

	// An unrecorded leg contributes nothing rather than a zero UUID — the gate
	// degrades to today's behavior for legs it has no plan for.
	aux := r.makeAuxRules(t, desc, 1)
	auxTpID := aux.Forward.NextTransportID()
	nrg := &NoiseRouteGroup{rg: rg}
	require.NoError(t, r.appendRouteToGroup(nrg, aux))
	require.Equal(t, []uuid.UUID{primaryTpID}, rg.remoteLegTransportIDs(),
		"a leg with no recorded reverse must not synthesize an exclusion")

	// Record the aux leg's far end, then drop the PRIMARY: the surviving leg's
	// entry must remain and the dropped one's must disappear.
	auxRemote := uuid.New()
	rg.recordLegRoute(
		[]routing.Hop{{TpID: auxTpID, From: local, To: peer}},
		[]routing.Hop{{TpID: auxRemote, From: peer, To: local}},
	)
	require.ElementsMatch(t, []uuid.UUID{primaryTpID, auxRemote}, rg.remoteLegTransportIDs())

	require.NoError(t, r.RemoveMuxRouteByTransport(descOf(t, r, rg, desc), primaryTpID))
	require.Equal(t, []uuid.UUID{auxRemote}, rg.remoteLegTransportIDs(),
		"a pruned leg must free its far-end transport immediately, or the group can never re-grow over that link")
}

// descOf registers rg under desc in rgsNs (RemoveMuxRouteByTransport looks the
// group up there) and returns desc.
func descOf(t *testing.T, r *router, rg *RouteGroup, desc routing.RouteDescriptor) routing.RouteDescriptor {
	t.Helper()
	r.mx.Lock()
	delete(r.rgsRaw, desc)
	r.rgsNs[desc] = &NoiseRouteGroup{rg: rg}
	r.mx.Unlock()
	return desc
}

// TestAddMuxRouteByHopsRejectsCollidingReverseBeforeDial is the behavior change.
// The planned leg has a perfectly good, disjoint FORWARD first hop — every
// pre-existing guard passes it — but its reverse leaves the destination over
// the transport the destination already holds the primary leg on. Before the
// fix this was dialed through the setup node (route-ID reservation on every hop
// plus intermediary rule installs) purely to be refused; now it is dropped
// locally and the setup node is never contacted.
func TestAddMuxRouteByHopsRejectsCollidingReverseBeforeDial(t *testing.T) {
	r := newLegTestRouter(t)
	rg, desc := r.setupInitializingPrimary(t)
	nrg := &NoiseRouteGroup{rg: rg}
	r.mx.Lock()
	delete(r.rgsRaw, desc)
	r.rgsNs[desc] = nrg
	r.mx.Unlock()

	local := desc.DstPK()
	peer := desc.SrcPK()

	rg.mu.Lock()
	primaryTpID := rg.tps[0].Entry.ID
	rg.mu.Unlock()
	// Primary: 1-hop direct in both directions — the shape that dominates the
	// live route_length_hist and the one that makes this collision the default.
	rg.recordLegRoute(
		[]routing.Hop{{TpID: primaryTpID, From: local, To: peer}},
		[]routing.Hop{{TpID: primaryTpID, From: peer, To: local}},
	)

	auxRules := r.makeAuxRules(t, desc, 1)
	auxTpID := auxRules.Forward.NextTransportID()
	dialer := &countingLegDialer{rules: auxRules}
	r.conf.RouteGroupDialer = dialer

	mid, _ := cipher.GenerateKeyPair()
	// Forward: 2-hop, disjoint first hop — passes the existing local guard.
	fwd := []routing.Hop{
		{TpID: auxTpID, From: local, To: mid},
		{TpID: uuid.New(), From: mid, To: peer},
	}
	// Reverse: the latency-cheapest 1-hop direct, which the finder picks
	// independently of the forward. Its first hop is the destination's own
	// direct transport — already carrying the primary leg over there.
	badRev := []routing.Hop{{TpID: primaryTpID, From: peer, To: local}}

	err := r.AddMuxRouteByHops(desc, fwd, badRev)
	require.Error(t, err, "a leg the destination is certain to refuse must not be committed")
	require.Contains(t, err.Error(), "already carries one of its legs")
	require.Zero(t, dialer.calls, "the doomed leg must be dropped BEFORE the setup-node dial — that dial is the churn")
	require.Equal(t, 1, legCount(rg), "the working primary leg is untouched by the rejection")

	// The SAME forward over a reverse that leaves the destination on a free
	// transport is still accepted: the gate rejects the collision, not the leg.
	goodRev := []routing.Hop{
		{TpID: uuid.New(), From: peer, To: mid},
		{TpID: auxTpID, From: mid, To: local},
	}
	require.NoError(t, r.AddMuxRouteByHops(desc, fwd, goodRev))
	require.Equal(t, 1, dialer.calls)
	require.Equal(t, 2, legCount(rg))
	require.ElementsMatch(t, []uuid.UUID{primaryTpID, goodRev[0].TpID}, rg.remoteLegTransportIDs(),
		"the committed leg claims its far-end transport so the NEXT leg diverges from it too")
}

// TestWarmRoutePoolSkipsPlanCollidingWithRemoteLeg covers the amplifier. The
// shared plan cache dedupes buckets on the FORWARD first hop only, so a plan
// whose reverse collides at the destination was re-served on every hit for its
// whole TTL — one bad plan turning into a steady stream of refused setups.
func TestWarmRoutePoolSkipsPlanCollidingWithRemoteLeg(t *testing.T) {
	p := newWarmRoutePool(defaultWarmPlanTTL)
	dst, _ := cipher.GenerateKeyPair()
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()

	fwdTp, revTp := uuid.New(), uuid.New()
	p.put(dst, 2,
		[]routing.Hop{{TpID: fwdTp, From: src, To: mid}, {TpID: uuid.New(), From: mid, To: dst}},
		[]routing.Hop{{TpID: revTp, From: dst, To: mid}, {TpID: uuid.New(), From: mid, To: src}},
	)

	// No far-end exclusions: the plan is served, as before.
	_, _, ok := p.bestPlan(dst, 2, nil, nil, nil)
	require.True(t, ok)

	// The caller's group already has a leg the destination holds on revTp.
	_, _, ok = p.bestPlan(dst, 2, nil, []uuid.UUID{revTp}, nil)
	require.False(t, ok, "a cached plan the destination would refuse must be a MISS, so the caller re-plans instead of re-dialing it")

	// A different far-end transport is not a collision.
	_, _, ok = p.bestPlan(dst, 2, nil, []uuid.UUID{uuid.New()}, nil)
	require.True(t, ok)
}

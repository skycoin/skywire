// Package appserver pkg/app/appserver/dial_app_knobs.go
//
// The dial path's read of the live per-app knobs.
//
// Two of them are the visor's business rather than the app's. `proxy mux
// cap|width` used to drive a pair of PROCESS-GLOBAL atomics in the routing
// policy preset, so pinning the legs of the proxy under test also pinned them
// on the paired reference app dialing beside it, and a bench could only record
// that contamination rather than avoid it. Here they are read from the knob set
// of the app that OWNS the dial (mux.width, mux.cap) and stamped on that dial
// alone; an app with neither set dials exactly as it does today, under the
// visor-wide adaptive values.
//
// The other two shape the standby pool's candidate search: the app passes
// pool.exclude_pks and pool.require_tp_types on the dial it issues (it is the
// app's pool, and the app knows which of its dials is a pool dial), and this
// file turns them into the router's existing peer-exclusion options and into
// the first-hop type check that runs once the dial has landed.
package appserver

import (
	"fmt"
	"strings"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// applyAppMuxKnobs stamps the OWNING app's per-app mux width and ceiling onto
// a dial that did not ask for a width of its own.
//
// "Did not ask" includes MuxRoutes == 1, and that is the case that matters: a
// skysocks tunnel passes 1 to say "form a route group rather than take the
// direct shortcut", never "one leg and no more" — its legs have always come
// from the adaptive engine afterwards. A count GREATER than one is a real
// choice (skynet-client's --routes, the CLI's --mux) and wins; the knob only
// bounds it.
//
// Both are clamped together: with a cap set and no width, an explicit request
// above the cap is pulled down to it; with both set, the width is what is
// asked for, bounded by the cap.
func applyAppMuxKnobs(m ProcManager, appName string, req *DialOptionsReq) {
	if m == nil || req == nil || appName == "" {
		return
	}
	vals, _, _, _ := m.AppSettingsState(appName)
	if len(vals) == 0 {
		return
	}
	width := int(vals[skysettings.MuxWidth])
	capN := int(vals[skysettings.MuxCap])
	if width <= 0 && capN <= 0 {
		return
	}
	if width > 0 && capN > 0 && width > capN {
		width = capN
	}
	if width > 0 && req.MuxRoutes <= 1 && req.ForwardMuxRoutes == 0 && req.ReverseMuxRoutes == 0 {
		req.MuxRoutes = width
	}
	if capN > 0 {
		clamp := func(n int) int {
			if n > capN {
				return capN
			}
			return n
		}
		req.MuxRoutes = clamp(req.MuxRoutes)
		req.ForwardMuxRoutes = clamp(req.ForwardMuxRoutes)
		req.ReverseMuxRoutes = clamp(req.ReverseMuxRoutes)
	}
}

// parsePKList turns the hex public keys an app passed into cipher.PubKeys,
// dropping anything that does not parse. The CLI validates on the way in, so a
// bad entry here is a newer visor talking to an older app, and a filter that
// refused to dial at all would be worse than one that excludes what it can.
func parsePKList(pks []string) []cipher.PubKey {
	out := make([]cipher.PubKey, 0, len(pks))
	for _, s := range pks {
		var pk cipher.PubKey
		if err := pk.Set(strings.TrimSpace(s)); err != nil {
			continue
		}
		out = append(out, pk)
	}
	return out
}

// ErrFirstHopTypeRefused is returned when a dial landed on a first hop whose
// transport type the caller's pool.require_tp_types does not admit. It is an
// ordinary dial failure: the standby pool's bounded retry answers it by asking
// again, which re-ranks and usually picks a different hop.
var ErrFirstHopTypeRefused = fmt.Errorf("first hop transport type not admitted by pool.require_tp_types")

// checkFirstHopType enforces a dial's first-hop transport-type requirement.
//
// The router's own candidate filtering by transport type is visor-wide config
// (routing.route_exclude_transport_types) and cannot express "this app's pool
// dials only", so the check is made on the far side of the dial: the route
// group that just came up is found by the local port it was dialed from, and
// its primary leg's type is compared against the requirement. A refusal closes
// the group, which is the only honest thing to do with a tunnel the operator
// said not to hold.
//
// Reports nil when there is no requirement, when the group cannot be resolved
// (nothing to judge — an accepted or already-gone group), or when the type is
// admitted.
func checkFirstHopType(rt router.Router, appName string, localPort routing.Port, want []string) error {
	if len(want) == 0 || rt == nil {
		return nil
	}
	for _, info := range rt.RouteGroupMuxInfoForApp(appName) {
		if uint16(info.Desc.DstPort()) != uint16(localPort) && //nolint:gosec
			uint16(info.Desc.SrcPort()) != uint16(localPort) { //nolint:gosec
			continue
		}
		if len(info.Legs) == 0 {
			return nil
		}
		got := strings.ToLower(info.Legs[0].TpType)
		for _, w := range want {
			if strings.EqualFold(strings.TrimSpace(w), got) {
				return nil
			}
		}
		return fmt.Errorf("%w: got %q, want one of %s", ErrFirstHopTypeRefused, got, strings.Join(want, "|"))
	}
	return nil
}

// routerFor resolves the router behind an address family's networker, the same
// way the dial does. nil when the family has no skynet networker (dmsg, or a
// test mock), which leaves every router-side check inert.
func routerFor(net appnet.Type) router.Router {
	nw, err := appnet.ResolveNetworker(net)
	if err != nil {
		return nil
	}
	sw, ok := nw.(*appnet.SkywireNetworker)
	if !ok {
		return nil
	}
	return sw.Router()
}

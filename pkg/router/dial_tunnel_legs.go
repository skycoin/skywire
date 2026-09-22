//go:build !tinygo || (js && wasm)

// Package router pkg/router/dial_tunnel_legs.go c2-net-routing
//
// Multi-leg tunnels AT DIAL TIME.
//
// An app tunnel (skysocks-client's `--tunnels N`, `--routed`) asks for
// MuxRoutes=1 — "form a route group, even a single-route one" — and its legs
// are bolted on afterwards, either by the operator (`proxy mux add`) or by the
// background self-heal growing toward the adaptive width. That costs the first
// seconds of every session: the tunnel carries on one leg while the others are
// still being planned and set up.
//
// The leg count is a VISOR-side decision (the mux width lives in the router's
// adaptive preset, not in the app process, so the app cannot read it), which is
// why the raise happens here rather than in the app's dial request.
package router

import "github.com/skycoin/skywire/pkg/router/policy/preset"

// applyDialTunnelLegs raises a tunnel's requested leg count to the visor's
// dial-time tunnel width, per the DialTunnelLegs knob:
//
//	 0 (default) — no change; the dial gets exactly what the app asked for.
//	-1           — the visor's mux width (preset.AdaptRevActive), i.e. the width
//	               the adaptive engine would have grown this group to anyway.
//	 n > 1       — n legs.
//
// Only a dial that asked for a SINGLE-ROUTE group is raised. A dial that named
// its own mux degree has expressed a preference and keeps it; a --direct dial
// is a 1-hop control route by definition (EffectiveMuxRoutes forces 1 for it
// regardless); a datagram dial does not support the mux fan-out; and a dial
// with no app name is not a tunnel. Returns the applied count, or 0 when
// nothing changed, so the caller can log the decision.
func applyDialTunnelLegs(opts *DialOptions) int {
	if opts == nil || opts.MuxRoutes != 1 || opts.ForwardMuxRoutes != 0 || opts.ReverseMuxRoutes != 0 {
		return 0
	}
	if opts.AppName == "" || opts.EnsureDirectTransport || opts.Datagram {
		return 0
	}
	if opts.TunnelRole == tunnelRoleStandby {
		// A pooled tunnel is dialed single-leg and stays that way; the width
		// belongs to the ACTIVE tunnels (pool_arbiter.go).
		return 0
	}
	want := dialTunnelLegsFor(opts.AppName)
	if want == 0 {
		return 0
	}
	if want < 0 {
		// TODO(mux): read the PER-APP mux width once the proxy mux ops expose
		// one (visor.SetMuxWidth is still process-global — preset.SetAdaptRevActive).
		want = preset.AdaptRevActive()
	}
	if want <= 1 {
		return 0
	}
	opts.MuxRoutes = want
	return want
}

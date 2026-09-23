// Package skysocks pkg/skysocks/client_shape.go
//
// The app half of the mux SHAPE axis (docs/design/mux-shape-axis.md step 6).
//
// A shape is "k tunnels of n legs". The router owns n — it composes and
// decomposes legs under one route group — and it can flip a standby group's
// ROLE to active when the shape asks for a wider k. What it cannot do is put a
// stream on the result: a tunnel is a yamux.Session over a route group, and
// that session belongs to this process. So k travels the other way, from the
// router to the app, and the app's active set follows it.
//
// The channel is the one the client already polls: the visor's per-tunnel
// snapshot (pkg/proxystatus), pulled every tunnel.prior_refresh for the
// capacity priors, now carries the session's ShapeTunnels and ShapeSource. No
// new RPC, no new tick, and nothing to send when the shape never moves.
package skysocks

import (
	"fmt"
	"math"
	"sort"

	"github.com/0magnet/yamux"
)

// shapeSourceMuxShape is router.shapeSourceKnob — the ShapeSource an operator's
// mux.shape wears, as opposed to "auto". Spelled out here because the router's
// copy is unexported and this package is downstream of it.
const shapeSourceMuxShape = "mux.shape"

// tunnelTarget is how many tunnels the active set is aiming at, and what said
// so. An explicit mux.shape WINS over tunnel.count: it is the operator naming
// the whole session's shape, and the router is already converging the leg half
// of it. Under "auto" — the default, and every client whose operator never
// touched the knob — this is tunnel.count exactly as before.
func (c *Client) tunnelTarget() (int, string) {
	c.redialMu.Lock()
	defer c.redialMu.Unlock()
	if c.shapeSource == shapeSourceMuxShape && c.shapeTunnels >= 1 {
		return c.shapeTunnels, c.shapeSource
	}
	return c.target, shapeSourceAuto
}

// shapeSourceAuto is the ShapeSource of a session no explicit shape governs.
const shapeSourceAuto = "auto"

// applyShapeTarget installs the shape target the visor's snapshot reported and
// says whether it moved. A snapshot that carries no shape at all (an older
// visor, a surface with no route group) leaves the target alone rather than
// clearing it: an absent field is not news.
func (c *Client) applyShapeTarget(k int, source string) bool {
	if source == "" {
		return false
	}
	c.redialMu.Lock()
	defer c.redialMu.Unlock()
	if c.shapeTunnels == k && c.shapeSource == source {
		return false
	}
	c.shapeTunnels, c.shapeSource = k, source
	return true
}

// reconcileShape is the convergence an explicit mux.shape needs on a TICK
// rather than on the settings change that started it: the tunnel the shape
// wants parked may be carrying a download for minutes, and it is only parkable
// once it falls idle. Inert under mux.shape=auto, which is every client whose
// operator never named a shape.
//
// The app's own freezes hold it, and hold the ROUTER's half too: the visor
// mirrors pool.freeze / tunnel.freeze_active into the router's mux.shape_hold
// (pkg/visor/api_app_settings.go), so a frozen app is frozen at both ends.
func (c *Client) reconcileShape() {
	if _, source := c.tunnelTarget(); source != shapeSourceMuxShape {
		return
	}
	if setPoolFreeze() || tunnelFreezeActive() {
		return
	}
	c.reconcileActiveSet("mux.shape convergence tick")
}

// markDraining marks the surplus ACTIVE tunnels — the ones above the target,
// worst RTT first — that are still carrying streams, so the picker stops
// assigning NEW streams to them. It is the step BEFORE a park, not an
// alternative to one: the tunnel keeps what it holds until it falls idle, and
// the next tick parks it then (I6/I7 — a park never costs a stream in flight).
//
// Exactly len(active)-target tunnels are marked, so the target's worth of
// tunnels is always left to carry.
func (c *Client) markDraining(target int, reason string) {
	c.sessionsMu.Lock()
	type ranked struct {
		s   *yamux.Session
		rtt float64
	}
	live := make([]ranked, 0, len(c.sessions))
	for _, s := range c.sessions {
		if s == nil || s.IsClosed() || c.standby[s] {
			continue
		}
		rtt, ok := c.meterRTTLocked(s)
		if !ok {
			rtt = math.MaxFloat64 // never measured: the least evidence of anything
		}
		live = append(live, ranked{s, rtt})
	}
	surplus := len(live) - target
	if surplus <= 0 {
		c.draining = nil
		c.sessionsMu.Unlock()
		return
	}
	sort.SliceStable(live, func(i, j int) bool { return live[i].rtt > live[j].rtt })
	next := make(map[*yamux.Session]bool, surplus)
	for i := 0; i < surplus; i++ {
		next[live[i].s] = true
	}
	changed := len(next) != len(c.draining)
	for s := range next {
		if !c.draining[s] {
			changed = true
			break
		}
	}
	c.draining = next
	c.sessionsMu.Unlock()
	if changed && c.appCl != nil {
		c.appCl.Log().Infof("Draining %d active tunnel(s) the shape wants parked (%s); they take no new stream", surplus, reason)
	}
}

// clearDraining lets every drained tunnel carry again — the target grew, or the
// active set already matches it.
func (c *Client) clearDraining() {
	c.sessionsMu.Lock()
	c.draining = nil
	c.sessionsMu.Unlock()
}

// drainingCount is how many active tunnels are being drained toward a park.
func (c *Client) drainingCount() int {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	return len(c.draining)
}

// shapeReason labels a reconcile with the target that asked for it.
func shapeReason(target int, source, reason string) string {
	if source == shapeSourceMuxShape {
		return fmt.Sprintf("reconcile: mux.shape wants %d active tunnel(s) (%s)", target, reason)
	}
	return fmt.Sprintf("reconcile: active set against tunnel.count=%d (%s)", target, reason)
}

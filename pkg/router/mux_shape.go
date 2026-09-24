// Package router pkg/router/mux_shape.go c2-net-routing
//
// The shape vocabulary. A SESSION is one (app, exit) pair; it owns k ACTIVE
// tunnels holding n_1..n_k legs, and that (k; n_1…n_k) is its SHAPE — "4x1"
// pure stream level (four tunnels, no packet striping), "1x4" pure packet
// level, "2x2" today's default (docs/design/mux-shape-axis.md).
//
// Read-only as shipped: this file MEASURES the shape a session has and reports
// the shape mux.shape asks for. Nothing here moves a chain; the converger is a
// later step, and until it lands the target is advisory.
package router

import (
	"sort"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routersettings"
)

// Where a session's shape TARGET came from, reported as MuxInfo.ShapeSource.
const (
	// shapeSourceAuto is the mux.shape=auto rule: tunnel.count tunnels, each
	// at the width pool.active_width / mux.app_width / the self-heal target
	// already holds it at.
	shapeSourceAuto = "auto"
	// shapeSourceKnob is an operator-named shape.
	shapeSourceKnob = "mux.shape"
)

// Shape is a session's multiplexing shape: one entry per ACTIVE tunnel,
// holding that tunnel's leg count. The zero Shape is "no active tunnel".
type Shape struct {
	// Legs is per-tunnel, widest first, so two sessions holding the same
	// multiset of tunnels print and compare the same.
	Legs []int
}

// newShape orders legs widest-first and drops the counts below one: a tunnel
// always has at least one leg (invariant I6).
func newShape(legs []int) Shape {
	if len(legs) == 0 {
		return Shape{}
	}
	out := make([]int, 0, len(legs))
	for _, n := range legs {
		if n < 1 {
			n = 1
		}
		out = append(out, n)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return Shape{Legs: out}
}

// uniformShape is the shape of k tunnels at n legs each — the "<k>x<n>" half
// of the grammar.
func uniformShape(k, n int) Shape {
	if k < 1 {
		return Shape{}
	}
	legs := make([]int, k)
	for i := range legs {
		legs[i] = n
	}
	return newShape(legs)
}

// parseShape reads an EXPLICIT shape ("<k>x<n>" or "n1,n2,…"); "auto" is not
// one and is refused. The grammar itself lives in routersettings, where the
// mux.shape knob validates a value before pkg/router ever sees it.
func parseShape(raw string) (Shape, error) {
	legs, err := routersettings.ParseShapeSpec(raw)
	if err != nil {
		return Shape{}, err
	}
	return newShape(legs), nil
}

// Tunnels is k.
func (s Shape) Tunnels() int { return len(s.Legs) }

// Chains is Σ n_i — the chains the shape spends on tunnels. The session's full
// chain budget is this plus its standby pool, and every shape MOVE conserves
// that total.
func (s Shape) Chains() int {
	n := 0
	for _, l := range s.Legs {
		n += l
	}
	return n
}

// String renders the shape the way parseShape takes it back: "<k>x<n>" when
// every tunnel is the same width, "n1,n2,…" otherwise, empty when there is no
// active tunnel.
func (s Shape) String() string { return routersettings.FormatShapeSpec(s.Legs) }

// Equal compares two shapes as multisets (newShape has already ordered them).
func (s Shape) Equal(o Shape) bool {
	if len(s.Legs) != len(o.Legs) {
		return false
	}
	for i := range s.Legs {
		if s.Legs[i] != o.Legs[i] {
			return false
		}
	}
	return true
}

// shapeSession keys one session: the dialing app and the exit it dials. An
// app's tunnels to two different exits are two sessions with two shapes.
type shapeSession struct {
	app  string
	exit cipher.PubKey
}

// sessionKeyFor names the session a route group belongs to: the app that DIALED
// it and the FAR END of its descriptor.
//
// Desc.Dst is the LOCAL visor on BOTH edges — the setup node hands each edge
// the descriptor that points at that edge (farEndPK, leg_rehome.go) — so keying
// on Desc.DstPK() put every tunnel an app holds, to ANY exit, in ONE session:
// a client dialing two exits had one session's target applied across both, so a
// 2x2 for two exits converged as four tunnels of one session. The arbiter keyed
// its buckets on Desc.SrcPK() and was right by accident; going through farEndPK
// makes every consumer agree and keeps working on a descriptor built either way
// round.
func sessionKeyFor(rg *RouteGroup) shapeSession {
	if rg == nil {
		return shapeSession{}
	}
	return shapeSession{app: rg.AppName(), exit: rg.farEndPK()}
}

// sessionKeyOf is the same key off a snapshot, which carries the far end its
// group measured (MuxInfo.FarEndPK). A snapshot built without one falls back to
// the descriptor's Src, which farEndPK falls back to as well.
func sessionKeyOf(in MuxInfo) shapeSession {
	exit := in.FarEndPK
	if exit.Null() {
		exit = in.Desc.SrcPK()
	}
	return shapeSession{app: in.AppName, exit: exit}
}

// shapeInput is what one route group contributes to its session beyond its own
// snapshot: the leg count the group is being held at (the auto rule's n for
// that tunnel) and the mux.shape value in force for it, per-app overrides
// resolved.
type shapeInput struct {
	legTarget int
	spec      string
}

// sessionShape measures the shape of one session from its groups' snapshots:
// one entry per ACTIVE tunnel, holding its alive leg count. Standby tunnels
// are the pool, not the shape.
func sessionShape(infos []MuxInfo) Shape {
	legs := make([]int, 0, len(infos))
	for i := range infos {
		if infos[i].TunnelRole != tunnelRoleActive {
			continue
		}
		legs = append(legs, aliveLegs(infos[i]))
	}
	return newShape(legs)
}

// aliveLegs counts the legs of one tunnel the way the CONVERGER counts them
// (aliveLegCount): a leg is a leg for as long as its transport is open. The two
// counts have to agree — a leg the converger QUIESCES for the few seconds of a
// split's ack wait is still a chain the tunnel holds, and counting it out made
// a session holding 1,2,1,1 render as "4x1" while the converger was still
// trying to move it. A group with no leg detail at all still counts as one,
// since a tunnel never has zero legs.
func aliveLegs(in MuxInfo) int {
	n := 0
	for _, l := range in.Legs {
		if l.Alive {
			n++
		}
	}
	if n < 1 {
		n = 1
	}
	return n
}

// applySessionShapes fills Shape / ShapeTarget / ShapeSource on every ACTIVE
// tunnel's snapshot, grouping the snapshots into sessions by (app, exit).
// in is parallel to infos — in[i] belongs to infos[i] — and may be shorter or
// nil, in which case the measured width stands in for the target.
//
// A standby or accept-side group keeps the fields empty: it has no session
// shape of its own, and an exit cannot see which of a client's tunnels are in
// standby anyway.
func applySessionShapes(infos []MuxInfo, in []shapeInput) {
	type session struct {
		idx    []int
		legs   []int
		widths []int
		spec   string
	}
	byKey := map[shapeSession]*session{}
	keys := make([]shapeSession, 0, len(infos))
	for i := range infos {
		if infos[i].TunnelRole != tunnelRoleActive {
			continue
		}
		// The session is keyed by the app that DIALED the tunnel, which is the
		// key shapeStep's ledger writes under. KnobApp is a different thing —
		// it is empty unless `route settings --app` set an override for that
		// app — so keying on it both merged two apps' tunnels into one session
		// and missed every move the converger had recorded, which is why
		// `proxy mux info` never printed last= or moves[].
		key := sessionKeyOf(infos[i])
		s := byKey[key]
		if s == nil {
			s = &session{spec: routersettings.ShapeAuto}
			byKey[key] = s
			keys = append(keys, key)
		}
		alive := aliveLegs(infos[i])
		width := alive
		if i < len(in) {
			if in[i].legTarget > 0 {
				width = in[i].legTarget
			}
			if in[i].spec != "" {
				s.spec = in[i].spec
			}
		}
		s.idx = append(s.idx, i)
		s.legs = append(s.legs, alive)
		s.widths = append(s.widths, width)
	}
	for _, key := range keys {
		s := byKey[key]
		cur := newShape(s.legs)
		target, source := shapeTarget(s.spec, s.widths)
		counts, last := shapeMoves.read(key)
		k := 0
		if source == shapeSourceKnob {
			k = target.Tunnels()
		}
		for _, i := range s.idx {
			infos[i].Shape = cur.String()
			infos[i].ShapeTarget = target.String()
			infos[i].ShapeSource = source
			infos[i].ShapeTunnels = k
			infos[i].MoveCounts = counts
			infos[i].LastMove = last
		}
	}
}

// shapeTarget resolves a session's target shape. "auto" is the shape today's
// knobs already aim at — one tunnel per active tunnel the app holds, each at
// the width pool.active_width / mux.app_width / the self-heal target gives it
// — so an operator who never touches mux.shape sees the target track what the
// session is already doing. An explicit value IS the target; a value that
// somehow fails to parse (a config written by a newer visor) falls back to
// auto rather than reporting a shape nothing means.
func shapeTarget(spec string, autoWidths []int) (Shape, string) {
	if spec != "" && !strings.EqualFold(strings.TrimSpace(spec), routersettings.ShapeAuto) {
		if s, err := parseShape(spec); err == nil {
			return s, shapeSourceKnob
		}
	}
	return newShape(autoWidths), shapeSourceAuto
}

// shapeLegTarget is the leg count this group is currently held at under the
// auto rule: the load-time muxWidthTarget, or pool.compose_idle's idle width
// when that is wider. Only an ACTIVE tunnel has one; everything else is 0.
func (rg *RouteGroup) shapeLegTarget() int {
	if rg == nil || rg.TunnelRole() != tunnelRoleActive {
		return 0
	}
	t := rg.muxWidthTarget()
	if w := rg.poolIdleWidth(); w > t {
		t = w
	}
	if t < 1 {
		t = 1
	}
	return t
}

// shapeSpec is the mux.shape value in force for this group: the app's own
// override when `route settings --app` set one, else the visor-wide value.
func (rg *RouteGroup) shapeSpec() string {
	if rg == nil {
		return routersettings.ShapeAuto
	}
	return rg.knobs().Text(routersettings.MuxShape)
}

// shapeInputOf collects what a group contributes to its session's shape.
func shapeInputOf(rg *RouteGroup) shapeInput {
	return shapeInput{legTarget: rg.shapeLegTarget(), spec: rg.shapeSpec()}
}

// Package proxystatus pkg/proxystatus/tunnelrole_test.go c4-app-web
package proxystatus

import (
	"fmt"
	"strings"
	"testing"

	"github.com/0magnet/bitree"
)

// tunnelRoleSrc is the source (this visor) PK every fixture below roots at.
const tunnelRoleSrc = "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"

// tunnelRoleExit is the shared exit PK of every fixture tunnel.
const tunnelRoleExit = "022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1"

// roleLeg builds a single ALIVE, leg-ACTIVE (not parked) mux leg through its own
// intermediate — what a standby tunnel's one leg actually looks like: inside its
// own route group it is striping, which is exactly why counting leg glyphs made
// the page report every tunnel as active.
func roleLeg(idx int, mid string, standby bool) Leg {
	return Leg{
		Index: idx, Alive: true, Standby: standby, RemotePK: mid,
		TransportID: "tp-" + mid, TpType: "stcpr",
		RouteLatencyMS: 50, GoodputDownBps: 1000,
		Hops: []Hop{
			{From: tunnelRoleSrc, To: mid, TpType: "stcpr", TpID: "tp-" + mid},
			{From: mid, To: tunnelRoleExit, TpType: "stcpr", TpID: "tp-exit-" + mid},
		},
	}
}

// standbyPoolSnap is the shape the operator reported: 8 single-leg tunnels, 2
// labeled active and 6 standby, every leg leg-active inside its own group.
func standbyPoolSnap() Snapshot {
	snap := Snapshot{Surface: SurfaceSkysocks, App: "skysocks-client", Running: true, SelfPK: tunnelRoleSrc}
	for i := 0; i < 8; i++ {
		role := RoleStandby
		if i < 2 {
			role = RoleActive
		}
		snap.Tunnels = append(snap.Tunnels, Tunnel{
			Index: i, ExitPK: tunnelRoleExit, MuxEnabled: true, Role: role,
			Legs: []Leg{roleLeg(0, fmt.Sprintf("02%064d", i), false)},
		})
	}
	snap.Legs = snap.Tunnels[0].Legs
	snap.MuxEnabled = true
	return snap
}

// splitTree separates a rendered tree's spine children into stream-header nodes
// and leg nodes, in order.
func splitTree(root *bitree.Node) (headers, legs []*bitree.Node) {
	for _, n := range root.Right {
		if strings.HasPrefix(strings.TrimSpace(n.Label), StreamHeaderGlyph) {
			headers = append(headers, n)
			continue
		}
		legs = append(legs, n)
	}
	return headers, legs
}

// TestTunnelRoleRendersOnStreamRowNotLegs is the regression this fixes: with 2
// active and 6 standby single-leg tunnels the page must show 2 active and 6
// standby TUNNELS — the role is drawn on the tunnel's own row, read from
// Tunnel.Role — while every leg row keeps its own leg-level glyph (all active
// here, since each is the only leg of its group).
func TestTunnelRoleRendersOnStreamRowNotLegs(t *testing.T) {
	root := RouteTree(standbyPoolSnap())
	headers, legs := splitTree(root)
	if len(headers) != 8 || len(legs) != 8 {
		t.Fatalf("got %d headers / %d legs, want 8 / 8", len(headers), len(legs))
	}
	for i, h := range headers {
		wantRole, otherRole := tunnelRoleLabel(RoleStandby), tunnelRoleLabel(RoleActive)
		if i < 2 {
			wantRole, otherRole = otherRole, wantRole
		}
		if !strings.Contains(h.Label, wantRole) {
			t.Errorf("tunnel %d row %q is missing its role %q", i, h.Label, wantRole)
		}
		if strings.Contains(h.Label, otherRole) {
			t.Errorf("tunnel %d row %q carries the wrong role %q", i, h.Label, otherRole)
		}
		// The row keeps everything it said before the role was added.
		if !strings.Contains(h.Label, "1 leg") || !strings.Contains(h.Label, "mux on") {
			t.Errorf("tunnel %d row lost its leg count / mux state: %q", i, h.Label)
		}
	}
	// The LEG rows must not flip: each is the sole, striping leg of its group.
	for i, l := range legs {
		if len(l.Left) == 0 {
			t.Fatalf("leg %d has no left summary", i)
		}
		sum := l.Left[0].Label
		if !strings.Contains(sum, GlyphActive) || strings.Contains(sum, GlyphStandby) {
			t.Errorf("leg %d summary should stay leg-active: %q", i, sum)
		}
	}
}

// TestTunnelRolePageMarkup proves the two facts reach the HTML the browser (and
// the ~1s WebSocket push, which re-renders the same live region) actually gets:
// a tunnel census split by role, and a state-tinted role token on each tunnel
// row.
func TestTunnelRolePageMarkup(t *testing.T) {
	page := string(Render(standbyPoolSnap()))
	for _, want := range []string{
		`<p class="tcount"><b>8 tunnels</b>`,
		`<span class="ok">2 active</span>, <span class="standby">6 standby</span>`,
		`<span class="trole standby">` + tunnelRoleLabel(RoleStandby) + `</span>`,
		`<span class="trole ok">` + tunnelRoleLabel(RoleActive) + `</span>`,
		`pre.bitree .trole.ok`, // the CSS that colors it
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	// The live fragment carries it too — the WebSocket path restreams this region.
	if !strings.Contains(string(RenderFragment(standbyPoolSnap())), `6 standby`) {
		t.Error("the live fragment must carry the tunnel role census")
	}
}

// TestActiveTunnelWithParkedLeg: a PARKED leg inside an ACTIVE tunnel is a
// different fact from a standby tunnel, and both stay visible — the tunnel row
// says active while the parked leg's own row keeps the standby glyph.
func TestActiveTunnelWithParkedLeg(t *testing.T) {
	snap := Snapshot{
		Surface: SurfaceSkysocks, SelfPK: tunnelRoleSrc,
		Tunnels: []Tunnel{{
			Index: 0, ExitPK: tunnelRoleExit, MuxEnabled: true, Role: RoleActive,
			Legs: []Leg{
				roleLeg(0, fmt.Sprintf("02%064d", 1), false),
				roleLeg(1, fmt.Sprintf("02%064d", 2), true), // parked leg
			},
		}},
	}
	headers, legs := splitTree(RouteTree(snap))
	if len(headers) != 1 || len(legs) != 2 {
		t.Fatalf("got %d headers / %d legs, want 1 / 2", len(headers), len(legs))
	}
	if !strings.Contains(headers[0].Label, tunnelRoleLabel(RoleActive)) {
		t.Errorf("the tunnel row should read active: %q", headers[0].Label)
	}
	var active, parked int
	for _, l := range legs {
		if len(l.Left) == 0 {
			t.Fatal("leg without a left summary")
		}
		if strings.Contains(l.Left[0].Label, GlyphStandby) {
			parked++
			continue
		}
		active++
	}
	if active != 1 || parked != 1 {
		t.Errorf("got %d active / %d parked leg rows, want 1 / 1", active, parked)
	}
}

// TestUnlabeledTunnelRendersAsBefore: an empty Role (a single-tunnel session, or
// a route group belonging to something else) shows no role word at all and no
// role split in the census.
func TestUnlabeledTunnelRendersAsBefore(t *testing.T) {
	snap := Snapshot{
		Surface: SurfaceSkysocks, SelfPK: tunnelRoleSrc,
		Tunnels: []Tunnel{
			{Index: 0, ExitPK: tunnelRoleExit, Legs: []Leg{roleLeg(0, fmt.Sprintf("02%064d", 1), false)}},
			{Index: 1, ExitPK: tunnelRoleExit, Legs: []Leg{roleLeg(0, fmt.Sprintf("02%064d", 2), false)}},
		},
	}
	snap.Legs = snap.Tunnels[0].Legs // back-compat mirror the visor fills in
	headers, _ := splitTree(RouteTree(snap))
	for i, h := range headers {
		if strings.Contains(h.Label, RoleActive) || strings.Contains(h.Label, RoleStandby) {
			t.Errorf("unlabeled tunnel %d should carry no role word: %q", i, h.Label)
		}
	}
	page := string(Render(snap))
	if !strings.Contains(page, `<p class="tcount"><b>2 tunnels</b></p>`) {
		t.Error("an unlabeled census should count tunnels without a role split")
	}
	if strings.Contains(page, `class="trole`) {
		t.Error("an unlabeled tunnel must not emit a role token")
	}
}

// TestRouteGraphStandbyTunnelEdges: the graph rules on the TUNNEL's role too —
// every edge of a standby tunnel is drawn inactive (dim + thin) even though its
// leg is striping inside its own group, and the edge carries the role so the
// tooltip can name both states.
func TestRouteGraphStandbyTunnelEdges(t *testing.T) {
	g := buildRouteGraph(standbyPoolSnap())
	if len(g.Links) == 0 {
		t.Fatal("no links built")
	}
	var act, stby int
	for _, l := range g.Links {
		switch l.TunnelRole {
		case RoleActive:
			act++
			if !l.Active {
				t.Errorf("an active tunnel's edge should be active: %+v", l)
			}
		case RoleStandby:
			stby++
			if l.Active {
				t.Errorf("a standby tunnel's edge must be inactive: %+v", l)
			}
			if l.Width != 0.6 || !strings.HasPrefix(l.Color, "rgba(") {
				t.Errorf("a standby tunnel's edge should be thin + dim: width=%v color=%q", l.Width, l.Color)
			}
			// Both facts stay readable: standby TUNNEL, active LEG.
			if !strings.Contains(l.Tip, "["+RoleStandby+"]") || !strings.Contains(l.Tip, "R[0] active") {
				t.Errorf("edge tip should name the tunnel role and the leg state: %q", l.Tip)
			}
		default:
			t.Errorf("edge lost its tunnel role: %+v", l)
		}
	}
	if act != 4 || stby != 12 { // 2 hops per leg, 1 leg per tunnel
		t.Errorf("got %d active / %d standby edges, want 4 / 12", act, stby)
	}
}

// TestRouteGraphParkedLegEdges: inside an ACTIVE tunnel only the parked leg's
// own edges go inactive — the striping leg's edges stay live.
func TestRouteGraphParkedLegEdges(t *testing.T) {
	g := buildRouteGraph(Snapshot{
		Surface: SurfaceSkysocks, SelfPK: tunnelRoleSrc,
		Tunnels: []Tunnel{{
			Index: 0, ExitPK: tunnelRoleExit, MuxEnabled: true, Role: RoleActive,
			Legs: []Leg{
				roleLeg(0, fmt.Sprintf("02%064d", 1), false),
				roleLeg(1, fmt.Sprintf("02%064d", 2), true),
			},
		}},
	})
	for _, l := range g.Links {
		parked := strings.Contains(l.Tip, "R[1] standby")
		if parked == l.Active {
			t.Errorf("parked=%v but edge Active=%v: %q", parked, l.Active, l.Tip)
		}
		if l.TunnelRole != RoleActive {
			t.Errorf("edge should carry the active tunnel role: %+v", l)
		}
	}
}

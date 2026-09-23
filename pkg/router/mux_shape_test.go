package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// Every shape the grammar accepts must survive parse -> String -> parse
// unchanged, and the canonical rendering of a uniform shape is "<k>x<n>": a
// comma list of equal counts is the same shape written the long way.
func TestParseShapeRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
		tunnels   int
		chains    int
	}{
		{raw: "2x2", want: "2x2", tunnels: 2, chains: 4},
		{raw: "4x1", want: "4x1", tunnels: 4, chains: 4},
		{raw: "1x4", want: "1x4", tunnels: 1, chains: 4},
		{raw: "1x1", want: "1x1", tunnels: 1, chains: 1},
		{raw: "1,1", want: "2x1", tunnels: 2, chains: 2},
		{raw: "2,1,1", want: "2,1,1", tunnels: 3, chains: 4},
		{raw: "1,3", want: "3,1", tunnels: 2, chains: 4},
		{raw: " 2X2 ", want: "2x2", tunnels: 2, chains: 4},
	} {
		tc := tc
		t.Run(tc.raw, func(t *testing.T) {
			s, err := parseShape(tc.raw)
			require.NoError(t, err)
			require.Equal(t, tc.want, s.String())
			require.Equal(t, tc.tunnels, s.Tunnels())
			require.Equal(t, tc.chains, s.Chains())

			again, err := parseShape(s.String())
			require.NoError(t, err)
			require.True(t, again.Equal(s), "re-parsing the rendering must give the same shape")
			require.Equal(t, s.String(), again.String())
		})
	}
	require.Equal(t, "3x2", uniformShape(3, 2).String())
	require.Equal(t, "", Shape{}.String(), "no active tunnel renders empty")
}

// A tunnel with no leg is not a tunnel (invariant I6), and "auto" is not an
// explicit shape: the knob accepts it, the parser does not.
func TestParseShapeRejectsZeroLegs(t *testing.T) {
	for _, raw := range []string{"0x2", "2x0", "2x", "x2", "foo", "", "2x2x2", "-1x2", "1,0", "1,-2", "1,,2", "2.5x1", "auto"} {
		_, err := parseShape(raw)
		require.Error(t, err, "%q must be refused", raw)
	}
}

// The measured shape counts ACTIVE tunnels and each one's ALIVE legs: a
// standby tunnel is the pool rather than the shape, and a leg parked in warm
// standby is held rather than spent.
func TestSessionShapeCountsTunnelsAndLegs(t *testing.T) {
	src, _, err := cipher.GenerateDeterministicKeyPair([]byte("shape-src"))
	require.NoError(t, err)
	exit, _, err := cipher.GenerateDeterministicKeyPair([]byte("shape-exit"))
	require.NoError(t, err)

	mk := func(role string, legs ...bool) MuxInfo {
		in := MuxInfo{
			Desc:       routing.NewRouteDescriptor(src, exit, 49150, 3),
			KnobApp:    "skysocks-client",
			TunnelRole: role,
		}
		for _, standby := range legs {
			in.Legs = append(in.Legs, MuxLeg{Standby: standby})
		}
		return in
	}

	// Two active tunnels of two live legs each, one standby pool tunnel: 2x2.
	infos := []MuxInfo{
		mk(tunnelRoleActive, false, false),
		mk(tunnelRoleActive, false, false),
		mk("standby", false),
	}
	require.Equal(t, "2x2", sessionShape(infos).String())

	// A leg parked in warm standby does not count; a tunnel never reads zero.
	infos = []MuxInfo{
		mk(tunnelRoleActive, false, true),
		mk(tunnelRoleActive, true, true),
		mk(tunnelRoleActive, false, false, false),
	}
	require.Equal(t, "3,1,1", sessionShape(infos).String())

	require.Equal(t, "", sessionShape([]MuxInfo{mk("standby", false)}).String())
	require.Equal(t, "", sessionShape(nil).String())
}

// The shape fields land on every ACTIVE tunnel of a session and nowhere else;
// sessions are keyed by (app, exit), so two apps — or one app to two exits —
// are two shapes. "auto" reports the width the tunnels are being held at,
// an explicit mux.shape value reports itself.
func TestApplySessionShapesReportsTargetAndSource(t *testing.T) {
	src, _, err := cipher.GenerateDeterministicKeyPair([]byte("shape-src"))
	require.NoError(t, err)
	exitA, _, err := cipher.GenerateDeterministicKeyPair([]byte("shape-exit-a"))
	require.NoError(t, err)
	exitB, _, err := cipher.GenerateDeterministicKeyPair([]byte("shape-exit-b"))
	require.NoError(t, err)

	mk := func(app string, exit cipher.PubKey, role string, legs int) MuxInfo {
		in := MuxInfo{
			Desc:       routing.NewRouteDescriptor(src, exit, 49150, 3),
			KnobApp:    app,
			TunnelRole: role,
		}
		in.Legs = make([]MuxLeg, legs)
		return in
	}

	infos := []MuxInfo{
		mk("skysocks-client", exitA, tunnelRoleActive, 1),
		mk("skysocks-client", exitA, tunnelRoleActive, 1),
		mk("skysocks-client", exitA, "standby", 1),
		mk("skysocks-client", exitB, tunnelRoleActive, 2),
		mk("other-app", exitA, tunnelRoleActive, 4),
	}
	in := []shapeInput{
		{legTarget: 2, spec: "auto"},
		{legTarget: 2, spec: "auto"},
		{},
		{legTarget: 2, spec: "auto"},
		{legTarget: 1, spec: "1,3"},
	}
	applySessionShapes(infos, in)

	// exit A, two one-leg tunnels held at pool.active_width=2: 2x1 -> 2x2.
	for _, i := range []int{0, 1} {
		require.Equal(t, "2x1", infos[i].Shape)
		require.Equal(t, "2x2", infos[i].ShapeTarget)
		require.Equal(t, shapeSourceAuto, infos[i].ShapeSource)
	}
	// The standby tunnel is the pool, not the shape.
	require.Empty(t, infos[2].Shape)
	require.Empty(t, infos[2].ShapeTarget)
	require.Empty(t, infos[2].ShapeSource)
	// The same app to another exit is another session.
	require.Equal(t, "1x2", infos[3].Shape)
	require.Equal(t, "1x2", infos[3].ShapeTarget)
	// An explicit shape is the target, whatever the auto width would have said.
	require.Equal(t, "1x4", infos[4].Shape)
	require.Equal(t, "3,1", infos[4].ShapeTarget)
	require.Equal(t, shapeSourceKnob, infos[4].ShapeSource)

	// With no parallel input at all the target falls back to what is measured.
	bare := []MuxInfo{mk("skysocks-client", exitA, tunnelRoleActive, 2)}
	applySessionShapes(bare, nil)
	require.Equal(t, "1x2", bare[0].Shape)
	require.Equal(t, "1x2", bare[0].ShapeTarget)
	require.Equal(t, shapeSourceAuto, bare[0].ShapeSource)
}

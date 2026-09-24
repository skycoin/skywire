// mux_shape_session_key_test.go: a session is keyed by the app and the FAR END
// of the route group, never by Desc.Dst — which is the LOCAL visor on both
// edges. Keying on Dst put every tunnel an app held, to ANY exit, in one
// session, so one app dialing two exits converged and reported as a single
// wider session instead of two shapes.
package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// The key helper, on the descriptor a dialed group really carries (peer ->
// local) and on the opposite orientation, plus the snapshot form.
func TestSessionKeyNamesTheFarEnd(t *testing.T) {
	local, _, err := cipher.GenerateDeterministicKeyPair([]byte("session-key-local"))
	require.NoError(t, err)
	peer, _, err := cipher.GenerateDeterministicKeyPair([]byte("session-key-peer"))
	require.NoError(t, err)

	dialed := &RouteGroup{
		desc:    routing.NewRouteDescriptor(peer, local, 3, 49153),
		localPK: local,
		appName: "skysocks-client",
	}
	reversed := &RouteGroup{
		desc:    routing.NewRouteDescriptor(local, peer, 49153, 3),
		localPK: local,
		appName: "skysocks-client",
	}
	want := shapeSession{app: "skysocks-client", exit: peer}
	require.Equal(t, want, sessionKeyFor(dialed))
	require.Equal(t, want, sessionKeyFor(reversed),
		"the local key decides the far end, not the descriptor's orientation")
	require.Equal(t, shapeSession{}, sessionKeyFor(nil))

	// The converger reads the key off its live groups; the arbiter buckets its
	// pools with the same helper, so the two must not be able to disagree.
	require.Equal(t, want, shapeSessionKey([]*RouteGroup{nil, dialed}))

	// The snapshot form: FarEndPK when the group measured one, the
	// descriptor's Src otherwise (which is what farEndPK falls back to).
	require.Equal(t, want, sessionKeyOf(MuxInfo{
		Desc: routing.NewRouteDescriptor(peer, local, 3, 49153), FarEndPK: peer, AppName: "skysocks-client",
	}))
	require.Equal(t, want, sessionKeyOf(MuxInfo{
		Desc: routing.NewRouteDescriptor(peer, local, 3, 49153), AppName: "skysocks-client",
	}))
}

// One app, TWO exits: two sessions, two shapes, two move histories. With the
// key taken from Desc.Dst — this visor on both groups — all four tunnels
// landed in one session and every one of them reported "2,2,1,1".
func TestApplySessionShapesSeparatesTwoExitsOfOneApp(t *testing.T) {
	local, _, err := cipher.GenerateDeterministicKeyPair([]byte("two-exit-local"))
	require.NoError(t, err)
	exitA, _, err := cipher.GenerateDeterministicKeyPair([]byte("two-exit-a"))
	require.NoError(t, err)
	exitB, _, err := cipher.GenerateDeterministicKeyPair([]byte("two-exit-b"))
	require.NoError(t, err)

	// The descriptor a DIALED group carries: it points at this visor.
	mk := func(exit cipher.PubKey, legs int) MuxInfo {
		in := MuxInfo{
			Desc:       routing.NewRouteDescriptor(exit, local, 3, 49153),
			FarEndPK:   exit,
			AppName:    "skysocks-client",
			TunnelRole: tunnelRoleActive,
		}
		in.Legs = make([]MuxLeg, legs)
		for i := range in.Legs {
			in.Legs[i].Alive = true
		}
		return in
	}

	keyA := shapeSession{app: "skysocks-client", exit: exitA}
	shapeMoves.note(keyA, MuxShapeMove{Move: MuxEventPoolLegReleased, From: "2,2", To: "2x1", At: time.Now()})
	t.Cleanup(func() {
		shapeMoves.mu.Lock()
		delete(shapeMoves.entries, keyA)
		shapeMoves.mu.Unlock()
	})

	infos := []MuxInfo{mk(exitA, 1), mk(exitA, 1), mk(exitB, 2), mk(exitB, 2)}
	in := []shapeInput{
		{legTarget: 1, spec: "2x1"},
		{legTarget: 1, spec: "2x1"},
		{legTarget: 2, spec: "2x2"},
		{legTarget: 2, spec: "2x2"},
	}
	applySessionShapes(infos, in)

	for _, i := range []int{0, 1} {
		require.Equal(t, "2x1", infos[i].Shape, "tunnel %d belongs to the exit-A session, not to one merged 2,2,1,1 session", i)
		require.Equal(t, "2x1", infos[i].ShapeTarget)
		require.NotNil(t, infos[i].LastMove, "the exit-A session carries its own move history")
	}
	for _, i := range []int{2, 3} {
		require.Equal(t, "2x2", infos[i].Shape, "tunnel %d belongs to the exit-B session, not to one merged 2,2,1,1 session", i)
		require.Equal(t, "2x2", infos[i].ShapeTarget)
		require.Nil(t, infos[i].LastMove, "exit B must not inherit exit A's moves")
	}
}

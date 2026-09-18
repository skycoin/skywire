// Package commands cmd/apps/skysocks-client/commands/tunnel_fill_test.go c4-app-proxy
package commands

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// pipeConn is one end of a net.Pipe, standing in for a dialed route-group conn.
func pipeConn(t *testing.T) net.Conn {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() {
		_ = a.Close() //nolint:errcheck
		_ = b.Close() //nolint:errcheck
	})
	return a
}

// The bug, at the layer that had it: with --tunnels 2 and a topology that can
// dial only ONE route, the extra tunnel's dial used to run before the app
// reported Running, so a start that could have served on its single tunnel sat
// in Starting until `proxy start --timeout 60` gave up.
//
// start() must therefore return while the extra dial is still in flight — the
// caller's very next statements are setAppStatus(Running) and ListenAndServe.
func TestTunnelFillStartDoesNotBlockTheStartupPath(t *testing.T) {
	release := make(chan struct{})
	dialed := make(chan struct{})
	wired := make(chan struct{})

	f := tunnelFill{
		ctx:     context.Background(),
		tunnels: 2,
		dial: func() (net.Conn, error) {
			close(dialed)
			<-release // the dial the single-route topology never answers
			return nil, errors.New("no route to the exit")
		},
		add:  func(net.Conn) error { return errors.New("must not be reached") },
		wire: func() { close(wired) },
	}

	done := make(chan struct{})
	go func() { f.start(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("start() blocked on the tunnel dial; the app cannot report Running")
	}

	<-dialed // the fill really is dialing, it just does it behind the start
	select {
	case <-wired:
		t.Fatal("the tunnel set was handed over before the fill finished")
	default:
	}
	close(release)
	select {
	case <-wired:
	case <-time.After(5 * time.Second):
		t.Fatal("the fill never handed the tunnel set to the client's own machinery")
	}
}

// A fill where every extra dial fails leaves the session on the one tunnel it
// has and still arms the Client's re-dial + standby pool, which own every later
// attempt. Nothing is added, nothing panics, and run() returns.
func TestTunnelFillDegradesToTheOneDialableRoute(t *testing.T) {
	var dials int
	wired := 0

	tunnelFill{
		ctx:     context.Background(),
		tunnels: 3,
		dial: func() (net.Conn, error) {
			dials++
			return nil, errors.New("no disjoint route")
		},
		add:  func(net.Conn) error { t.Fatal("a failed dial must not be added"); return nil },
		wire: func() { wired++ },
	}.run()

	require.Equal(t, 2, dials, "every remaining tunnel is attempted once")
	require.Equal(t, 1, wired, "re-dial and the standby pool are armed exactly once")
}

// The healthy path is unchanged: every tunnel that dials joins the session, one
// at a time (the sequential order is what makes the visor's sibling exclusion
// steer tunnel i+1 off tunnel i's first hop), and the hand-over happens after.
func TestTunnelFillAddsEveryDialableTunnelSequentially(t *testing.T) {
	var (
		mu       sync.Mutex
		inFlight int
		added    int
		order    []string
	)

	tunnelFill{
		ctx:     context.Background(),
		tunnels: 3,
		dial: func() (net.Conn, error) {
			mu.Lock()
			inFlight++
			require.Equal(t, 1, inFlight, "tunnel dials must not overlap")
			order = append(order, "dial")
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
			return pipeConn(t), nil
		},
		add: func(net.Conn) error {
			mu.Lock()
			added++
			order = append(order, "add")
			mu.Unlock()
			return nil
		},
		wire: func() {
			mu.Lock()
			order = append(order, "wire")
			mu.Unlock()
		},
	}.run()

	require.Equal(t, 2, added)
	require.Equal(t, []string{"dial", "add", "dial", "add", "wire"}, order)
}

// A single-tunnel client dials nothing extra and wires nothing: --tunnels 1 is
// byte-identical to the pre-aggregation path.
func TestTunnelFillIsANoOpForASingleTunnel(t *testing.T) {
	tunnelFill{
		ctx:     context.Background(),
		tunnels: 1,
		dial:    func() (net.Conn, error) { t.Fatal("a single-tunnel client dials nothing extra"); return nil, nil },
		add:     func(net.Conn) error { t.Fatal("unreachable"); return nil },
		wire:    func() {},
	}.run()
}

// A cycle torn down mid-fill (visor stop, SIGINT, reconnect) stops dialing at
// the next tunnel rather than running the whole width out against a dead ctx.
func TestTunnelFillStopsWhenTheCycleIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var dials int

	tunnelFill{
		ctx:     ctx,
		tunnels: 4,
		dial: func() (net.Conn, error) {
			dials++
			cancel()
			return nil, errors.New("cycle going away")
		},
		add:  func(net.Conn) error { return nil },
		wire: func() {},
	}.run()

	require.Equal(t, 1, dials, "the fill stops at the first tunnel after cancellation")
}

// Only the session's first tunnel may give up its route group, and only when
// nothing asked for one by name. This is the policy the e2e regression turned
// on: `--tunnels 2` being the default made tunnel 1 a route-group dial where it
// used to be the AppDirect shortcut, and on a topology that can build no route
// group the session never came up at all.
func TestFirstTunnelGroupDecaysOnlyForTheImplicitUpgrade(t *testing.T) {
	two := func() *clientConfig { return &clientConfig{tunnels: 2} }

	require.True(t, firstTunnelGroupDecays(two(), false, false),
		"the default --tunnels 2 asked for the route group, not the operator")

	require.False(t, firstTunnelGroupDecays(&clientConfig{tunnels: 1}, false, false),
		"--tunnels 1 asks for no route group, so there is nothing to decay")

	routed := two()
	routed.routed = true
	require.False(t, firstTunnelGroupDecays(routed, false, false), "--routed names the route group")

	direct := two()
	direct.direct = true
	require.False(t, firstTunnelGroupDecays(direct, false, false), "--direct is the shortcut already")

	require.False(t, firstTunnelGroupDecays(two(), true, false),
		"a widening or re-dial is not the session's only tunnel")
	require.False(t, firstTunnelGroupDecays(two(), false, true),
		"a standby-pool dial is not the session's only tunnel")
	require.False(t, firstTunnelGroupDecays(nil, false, false))
}

// The ceiling that bounds the implicit upgrade is a live knob with a real
// default, not a bare constant: an operator can widen or disable the decay on a
// running client without restarting it.
func TestFirstTunnelGroupCeilingIsALiveKnob(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	require.Equal(t, 20*time.Second, skysettings.Dur(skysettings.TunnelGroupDialCeiling))
	require.True(t, skysettings.Apply(map[string]int64{
		skysettings.TunnelGroupDialCeiling: int64(45 * time.Second),
	}))
	require.Equal(t, 45*time.Second, skysettings.Dur(skysettings.TunnelGroupDialCeiling))
}

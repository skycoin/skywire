// Package appnet pkg/app/appnet/skywire_networker_router_test.go: tests that
// drive the SkywireNetworker dial/ping/packet/serve paths through a stub
// router.Router whose dial methods return errors — exercising the routed
// fallback paths (and the nil-mux direct-dial shortcuts) without a live stack.
package appnet

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

// stubRouter embeds router.Router (nil) so it satisfies the whole interface;
// only the dial/accept methods the networker calls are overridden. Any
// unexpected call panics, which is the desired loud failure.
type stubRouter struct {
	router.Router
	err error
}

func (s stubRouter) DialRoutes(context.Context, cipher.PubKey, routing.Port, routing.Port, *router.DialOptions) (net.Conn, error) {
	return nil, s.err
}

// The dial path asks this before deciding whether a direct 0-hop conn is
// acceptable. 1 = "one hop is fine", which is what these tests assume: they
// exercise the fallback to DialRoutes when no direct transport exists, not
// the min-hops constraint. Embedding router.Router alone would leave it nil
// and panic here rather than fail a comparison.
func (s stubRouter) EffectiveMinHops(*router.DialOptions) uint16 {
	return 1
}

func (s stubRouter) PingRoute(context.Context, cipher.PubKey, routing.Port, routing.Port, *router.DialOptions) (net.Conn, error) {
	return nil, s.err
}

func (s stubRouter) DialRoutesDatagram(context.Context, cipher.PubKey, routing.Port, routing.Port, *router.DialOptions) (net.Conn, *router.DatagramRouteGroup, error) {
	return nil, nil, s.err
}

func (s stubRouter) AcceptRoutes(context.Context) (net.Conn, error) {
	return nil, s.err
}

func stubNetworker(err error) *SkywireNetworker {
	r, _ := NewSkywireNetworker(logging.MustGetLogger("sn-router-test"), stubRouter{err: err}).(*SkywireNetworker)
	return r
}

func TestSkywireNetworker_DialRoutedFallback(t *testing.T) {
	r := stubNetworker(errors.New("dial routes failed"))
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 5}

	// No AppDirectMux → tryDirectDial returns false; falls through to
	// DialRoutes, which errors.
	_, err := r.Dial(addr)
	require.Error(t, err)

	_, err = r.DialContext(context.Background(), addr)
	require.Error(t, err)
}

func TestSkywireNetworker_PingRoutedFallback(t *testing.T) {
	r := stubNetworker(errors.New("ping route failed"))
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 5}

	_, err := r.Ping(pk, addr)
	require.Error(t, err)

	_, err = r.PingContext(context.Background(), pk, addr)
	require.Error(t, err)
}

func TestSkywireNetworker_DialPacketContext(t *testing.T) {
	r := stubNetworker(errors.New("datagram dial failed"))
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 5}

	_, err := r.DialPacketContext(context.Background(), addr)
	require.Error(t, err)
}

func TestSkywireNetworker_ServeRouteGroupStops(t *testing.T) {
	// AcceptRoutes returns a net.ErrClosed-wrapped error → serveRouteGroup
	// recognizes the terminal condition and returns instead of looping.
	r := stubNetworker(ErrClosedConn)
	err := r.serveRouteGroup(context.Background())
	require.Error(t, err)
}

// TestSkywireNetworker_DirectShortcutEligible locks the mux_routes=1 fix: the
// 0-hop direct shortcut is taken ONLY for an unset mux (MuxRoutes==0), so an
// explicit single route (MuxRoutes==1) forms a route group instead of silently
// collapsing to a bare direct conn. mux>=2 also skips the shortcut (unchanged).
// stubRouter.EffectiveMinHops returns 1, so min-hops never gates here.
func TestSkywireNetworker_DirectShortcutEligible(t *testing.T) {
	r := stubNetworker(nil)
	cases := []struct {
		mux  int
		want bool
	}{
		{0, true},  // unset → fast path
		{1, false}, // explicit single route → route group
		{2, false}, // mux → route group
		{4, false},
	}
	for _, c := range cases {
		got := r.directShortcutEligible(&router.DialOptions{MuxRoutes: c.mux})
		if got != c.want {
			t.Errorf("directShortcutEligible(MuxRoutes=%d) = %v, want %v", c.mux, got, c.want)
		}
	}
}

func TestSkywireNetworker_TryDirectDialNoMux(t *testing.T) {
	r := stubNetworker(nil)
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 5}

	_, ok := r.tryDirectDial(addr, "app")
	require.False(t, ok)

	_, ok = r.tryDirectPingDial(addr, nil)
	require.False(t, ok)
}

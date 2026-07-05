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

func TestSkywireNetworker_TryDirectDialNoMux(t *testing.T) {
	r := stubNetworker(nil)
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 5}

	_, ok := r.tryDirectDial(addr, "app")
	require.False(t, ok)

	_, ok = r.tryDirectPingDial(addr, nil)
	require.False(t, ok)
}

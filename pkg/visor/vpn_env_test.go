// Package visor pkg/visor/vpn_env_test.go
package visor

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/routing"
)

type fakeCarrierSession struct {
	carrier string
	remote  net.Addr
}

func (s fakeCarrierSession) Carrier() string         { return s.carrier }
func (s fakeCarrierSession) RemoteTCPAddr() net.Addr { return s.remote }

// The vpn-client is handed every dmsg server it must keep reachable outside
// the tunnel, and resolves each one. A skynet-carried session's remote is the
// server's key and port, which resolves to nothing — the phone's vpn-client
// refused to start on it with "lookup <pk>: no such host".
func TestVPNDirectDmsgAddr(t *testing.T) {
	t.Run("a directly dialed server is routed around the tunnel", func(t *testing.T) {
		addr, ok := vpnDirectDmsgAddr(fakeCarrierSession{
			carrier: dmsg.CarrierTCP,
			remote:  &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 8080},
		})
		require.True(t, ok)
		require.Equal(t, "203.0.113.7:8080", addr)
	})

	t.Run("a skynet-carried server has no address to route", func(t *testing.T) {
		pk, _ := cipher.GenerateKeyPair()
		remote := appnet.Addr{Net: appnet.TypeSkynet, PubKey: pk, Port: routing.Port(70)}
		addr, ok := vpnDirectDmsgAddr(fakeCarrierSession{carrier: dmsg.CarrierSkynet, remote: remote})
		require.False(t, ok, "would have handed the vpn-client %q to resolve", remote.String())
		require.Empty(t, addr)
	})
}

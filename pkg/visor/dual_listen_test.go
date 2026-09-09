package visor

import (
	"net"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/routing"
)

// A skynet mirror listener hands out conns of two shapes: a direct
// app-dial stream (appnet.Addr) and a route-group conn (routing.Addr).
// Both must identify the peer, or the hypervisor rejects the conn.
func TestRemotePKAcceptsEveryAcceptedConnShape(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	for name, addr := range map[string]net.Addr{
		"dmsg":        dmsg.Addr{PK: pk, Port: 46},
		"direct":      appnet.Addr{Net: appnet.TypeSkynet, PubKey: pk, Port: 46},
		"route-group": routing.Addr{PubKey: pk, Port: 46},
	} {
		got, ok := remotePK(addr)
		if !ok || got != pk {
			t.Fatalf("%s: remotePK = (%s, %v), want (%s, true)", name, got, ok, pk)
		}
	}
	if _, ok := remotePK(&net.TCPAddr{}); ok {
		t.Fatal("a TCP addr must not identify a peer")
	}
}

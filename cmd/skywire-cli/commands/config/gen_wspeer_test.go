package cliconfig

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TestConfigureWSPeers pins --ws-peer: each <pk>@<ws(s)://…> lands in
// transport.ws_table (so the WS dialer never asks the address resolver for it)
// AND in persistent_transports as a WS transport (so the visor opens it at
// boot and re-dials it). This is the attached desk's one transport back to the
// hypervisor that served it.
func TestConfigureWSPeers(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	prev := conf
	t.Cleanup(func() { conf = prev })
	conf = new(visorconfig.V1)
	wsPeers = pk1.Hex() + "@ws://node.local:8000/tp/ws, " + pk2.Hex() + "@wss://example.org/tp/ws,"
	configureWSPeers(logging.MustGetLogger("test"))
	if conf.Transport == nil {
		t.Fatal("transport section not created")
	}
	if got := conf.Transport.WSTable[pk1]; got != "ws://node.local:8000/tp/ws" {
		t.Errorf("ws_table[pk1]=%q", got)
	}
	if got := conf.Transport.WSTable[pk2]; got != "wss://example.org/tp/ws" {
		t.Errorf("ws_table[pk2]=%q", got)
	}
	if len(conf.PersistentTransports) != 2 {
		t.Fatalf("persistent_transports=%d, want 2", len(conf.PersistentTransports))
	}
	for i, pk := range []cipher.PubKey{pk1, pk2} {
		if pt := conf.PersistentTransports[i]; pt.PK != pk || pt.NetType != tptypes.WS {
			t.Errorf("persistent_transports[%d]=%+v, want %s over %s", i, pt, pk, tptypes.WS)
		}
	}
}

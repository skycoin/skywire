package visor

import (
	"testing"

	"github.com/skycoin/skywire/pkg/dmsg/dmsgc/spec"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

const testDmsgdPK = "022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa"

func visorWithDmsg(discovery, discoveryDmsg string) *Visor {
	return &Visor{conf: &visorconfig.V1{
		Dmsg: &spec.DmsgConfig{
			Discovery:     discovery,
			DiscoveryDmsg: discoveryDmsg,
		},
	}}
}

// TestDmsgdCXOPeerFallsBackToDiscovery is the live bug this fixes. On a
// dmsg-only deployment the discovery PK lives in `discovery` (already a
// dmsg:// URL) and `discovery_dmsg` is unset. Reading only the latter made the
// clients-by-server feed spec fail with "no DMSG-D CXO peer", so enabling
// dmsg.lookup_cxo silently did nothing and every peer lookup fell back to HTTP
// — the feature was unreachable in the deployment it ships against.
func TestDmsgdCXOPeerFallsBackToDiscovery(t *testing.T) {
	v := visorWithDmsg("dmsg://"+testDmsgdPK+":80", "")
	pk, ok := dmsgdCXOPeer(v)
	if !ok {
		t.Fatal("no peer resolved from a dmsg:// discovery URL")
	}
	if pk.Hex() != testDmsgdPK {
		t.Errorf("peer = %s, want %s", pk.Hex(), testDmsgdPK)
	}
}

// TestDmsgdCXOPeerPrefersDiscoveryDmsg pins the ordering: when both are set the
// explicit field wins, matching tpdCXOPeer.
func TestDmsgdCXOPeerPrefersDiscoveryDmsg(t *testing.T) {
	other := "0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
	v := visorWithDmsg("dmsg://"+other+":80", "dmsg://"+testDmsgdPK+":80")
	pk, ok := dmsgdCXOPeer(v)
	if !ok {
		t.Fatal("no peer resolved")
	}
	if pk.Hex() != testDmsgdPK {
		t.Errorf("peer = %s, want the discovery_dmsg value %s", pk.Hex(), testDmsgdPK)
	}
}

// TestDmsgdCXOPeerIgnoresClearnetDiscovery guards the fallback: a clearnet
// discovery URL names no dmsg peer and must not be accepted as one.
func TestDmsgdCXOPeerIgnoresClearnetDiscovery(t *testing.T) {
	v := visorWithDmsg("http://dmsgd.skywire.skycoin.com", "")
	if _, ok := dmsgdCXOPeer(v); ok {
		t.Error("a clearnet discovery URL was accepted as a dmsg CXO peer")
	}
}

// TestDmsgdCXOPeerNoDmsgConfig covers the nil path.
func TestDmsgdCXOPeerNoDmsgConfig(t *testing.T) {
	v := &Visor{conf: &visorconfig.V1{}}
	if _, ok := dmsgdCXOPeer(v); ok {
		t.Error("a peer was resolved with no dmsg config")
	}
}

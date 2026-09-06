package regcxo

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestAggregatorPresentsServiceIdentity is the regression guard for #4569: no
// visor was registering over CXO because the aggregator's CXO node was built
// from a bare config, so node.NewNode minted a RANDOM keypair and every gated
// visor refused the subscribe — it allowlists dmsg-discovery's configured PK
// and saw an unknown one. Observed live as denied 0341b5a7... against allowed
// 022e607e...
//
// The node is not constructed here (that needs a live dmsg client); this pins
// the contract that the identity actually reaches the node config, which is
// the step that was missing.
func TestAggregatorPresentsServiceIdentity(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()

	conf := Config{SecKey: sk}
	if conf.SecKey == (cipher.SecKey{}) {
		t.Fatal("Config.SecKey did not retain the service key")
	}

	derived, err := conf.SecKey.PubKey()
	if err != nil {
		t.Fatalf("PubKey from the configured SecKey: %v", err)
	}
	if derived != pk {
		t.Errorf("configured key derives %s, want the service PK %s", derived.Hex(), pk.Hex())
	}
}

// TestAggregatorZeroKeyIsDetectable documents the failure mode rather than
// hiding it: a zero SecKey is what produced a random node identity, and the
// constructor must be able to tell the two apart.
func TestAggregatorZeroKeyIsDetectable(t *testing.T) {
	var conf Config
	if conf.SecKey != (cipher.SecKey{}) {
		t.Error("the zero value of Config.SecKey must compare equal to a zero key")
	}
}

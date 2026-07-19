// Package deployment deployment/config_test.go
package deployment

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// mustPK parses a hex public key or fails the test.
func mustPK(t *testing.T, hex string) cipher.PubKey {
	t.Helper()

	var pk cipher.PubKey
	if err := pk.Set(hex); err != nil {
		t.Fatalf("parse pk %s: %v", hex, err)
	}

	return pk
}

// The embedded server set must stay coupled to the discovery it was published
// with. A dmsg-server belonging to a private or e2e deployment asking for
// servers must get nothing rather than the production fleet — otherwise it opens
// sessions to public servers on its cold-start path, which is both a leak out of
// the deployment and contention against its own registration.
func TestEmbeddedServersForDiscoveryPK_OnlyMatchingDeployment(t *testing.T) {
	prodDiscPK := mustPK(t, "022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa")
	// The docker/integration (e2e) discovery — deliberately not a known deployment.
	e2eDiscPK := mustPK(t, "02857a87410b3709eddb99bc00508cc7cfeb588d5ed87e3ef9d2aac838ad49470a")

	t.Run("known discovery yields its own servers", func(t *testing.T) {
		got := EmbeddedServersForDiscoveryPK(prodDiscPK)
		if len(got) == 0 {
			t.Fatal("prod discovery PK returned no embedded servers")
		}
		// Every entry handed back must actually be a server entry.
		for _, e := range got {
			if e == nil || e.Server == nil {
				t.Fatalf("entry %+v is not a server entry", e)
			}
		}
	})

	t.Run("foreign discovery yields nothing", func(t *testing.T) {
		if got := EmbeddedServersForDiscoveryPK(e2eDiscPK); got != nil {
			t.Fatalf("e2e discovery PK leaked %d embedded servers; "+
				"embedded servers must stay coupled to their own discovery", len(got))
		}
	})

	t.Run("zero key yields nothing", func(t *testing.T) {
		if got := EmbeddedServersForDiscoveryPK(cipher.PubKey{}); got != nil {
			t.Fatalf("zero PK returned %d servers, want nil", len(got))
		}
	})

	t.Run("URL form agrees with PK form", func(t *testing.T) {
		byURL := EmbeddedServersForDiscoveryDmsg("dmsg://" + prodDiscPK.Hex() + ":80")
		byPK := EmbeddedServersForDiscoveryPK(prodDiscPK)
		if len(byURL) != len(byPK) {
			t.Fatalf("URL form returned %d servers, PK form %d", len(byURL), len(byPK))
		}
	})
}

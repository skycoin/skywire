package visor

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// whitelisted reports whether pk is on wl, failing the test on a
// whitelist error.
func whitelisted(t *testing.T, wl pty.Whitelist, pk cipher.PubKey) bool {
	t.Helper()
	ok, err := wl.Get(pk)
	if err != nil {
		t.Fatalf("whitelist Get(%s): %v", pk, err)
	}
	return ok
}

// TestNewPeerWhitelist_SeedsConfig verifies the shared whitelist is
// seeded from the visor's own PK, its hypervisors, and its configured
// pty whitelist — and nothing else.
func TestNewPeerWhitelist_SeedsConfig(t *testing.T) {
	ownPK, _ := cipher.GenerateKeyPair()
	hvPK, _ := cipher.GenerateKeyPair()
	ptyPK, _ := cipher.GenerateKeyPair()
	otherPK, _ := cipher.GenerateKeyPair()

	conf := &visorconfig.V1{Common: &visorconfig.Common{PK: ownPK}}
	conf.Hypervisors = []cipher.PubKey{hvPK}
	conf.Pty = &visorconfig.Pty{Whitelist: []cipher.PubKey{ptyPK}}

	wl := newPeerWhitelist(conf)

	for _, pk := range []cipher.PubKey{ownPK, hvPK, ptyPK} {
		if !whitelisted(t, wl, pk) {
			t.Errorf("expected %s to be whitelisted", pk)
		}
	}
	if whitelisted(t, wl, otherPK) {
		t.Errorf("unconfigured PK %s should not be whitelisted", otherPK)
	}
}

// TestVisor_AddPtyWhitelist verifies the transitive-trust mutation:
// a PK pushed via AddPtyWhitelist becomes part of the live shared
// whitelist, and nil/empty input is a safe no-op.
func TestVisor_AddPtyWhitelist(t *testing.T) {
	ownPK, _ := cipher.GenerateKeyPair()
	conf := &visorconfig.V1{Common: &visorconfig.Common{PK: ownPK}}
	v := &Visor{
		log:           logging.NewMasterLogger().PackageLogger("test"),
		peerWhitelist: newPeerWhitelist(conf),
	}

	transitivePK, _ := cipher.GenerateKeyPair()
	if whitelisted(t, v.peerWhitelist, transitivePK) {
		t.Fatal("precondition failed: transitivePK should not be whitelisted yet")
	}

	if err := v.AddPtyWhitelist([]cipher.PubKey{transitivePK}); err != nil {
		t.Fatalf("AddPtyWhitelist: %v", err)
	}
	if !whitelisted(t, v.peerWhitelist, transitivePK) {
		t.Error("transitivePK should be whitelisted after AddPtyWhitelist")
	}

	// nil and empty are safe no-ops.
	if err := v.AddPtyWhitelist(nil); err != nil {
		t.Errorf("AddPtyWhitelist(nil): %v", err)
	}
	if err := v.AddPtyWhitelist([]cipher.PubKey{}); err != nil {
		t.Errorf("AddPtyWhitelist(empty): %v", err)
	}
}

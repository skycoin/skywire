package visor

import (
	"context"
	"sync"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestEnsureDirectDmsgEntry verifies that the dmsg-tracker resolver hook
// makes a previously-unknown peer resolvable on the direct-client path,
// advertising the known dmsg servers as delegated — so the subsequent
// dial doesn't depend on the peer having a dmsg-discovery entry.
func TestEnsureDirectDmsgEntry(t *testing.T) {
	srvPK, _ := cipher.GenerateKeyPair()
	servers := []*dmsgdisc.Entry{{Static: srvPK}}

	// Direct client seeded with the server only — not the peer.
	dc := direct.NewClient(direct.GetAllEntries(cipher.PubKeys{}, servers),
		logging.NewMasterLogger().PackageLogger("test"))

	v := &Visor{
		log:               logging.NewMasterLogger().PackageLogger("test"),
		initLock:          new(sync.RWMutex),
		dClient:           dc,
		dmsgDirectServers: servers,
	}

	peerPK, _ := cipher.GenerateKeyPair()

	// Precondition: peer is not resolvable on the direct client.
	if _, err := dc.Entry(context.Background(), peerPK); err == nil {
		t.Fatal("precondition failed: peer should not be resolvable yet")
	}

	v.ensureDirectDmsgEntry(peerPK)

	e, err := dc.Entry(context.Background(), peerPK)
	if err != nil {
		t.Fatalf("peer should resolve after ensureDirectDmsgEntry: %v", err)
	}
	if e.Client == nil || len(e.Client.DelegatedServers) != 1 || e.Client.DelegatedServers[0] != srvPK {
		t.Errorf("synthetic entry should delegate the known server %s, got %+v", srvPK, e.Client)
	}
}

// TestEnsureDirectDmsgEntry_NoServers is a no-op when the direct client
// isn't up (no servers stashed) — it must not panic or register anything.
func TestEnsureDirectDmsgEntry_NoServers(t *testing.T) {
	v := &Visor{
		log:      logging.NewMasterLogger().PackageLogger("test"),
		initLock: new(sync.RWMutex),
	}
	peerPK, _ := cipher.GenerateKeyPair()
	v.ensureDirectDmsgEntry(peerPK) // must not panic
}

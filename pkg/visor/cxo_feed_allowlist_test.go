package visor

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// contains reports whether pks includes pk.
func contains(pks []cipher.PubKey, pk cipher.PubKey) bool {
	for _, p := range pks {
		if p == pk {
			return true
		}
	}
	return false
}

// TestComposeFeedAllowlist_StatsIncludesTPDAndPeers verifies the composed
// allowlist for a service-consumed feed is exactly the peer whitelist
// (own PK + hypervisors + pty whitelist) plus the consuming service's PK
// (the TPD, for the stats/tp-list feeds).
func TestComposeFeedAllowlist_StatsIncludesTPDAndPeers(t *testing.T) {
	ownPK, _ := cipher.GenerateKeyPair()
	hvPK, _ := cipher.GenerateKeyPair()
	ptyPK, _ := cipher.GenerateKeyPair()
	tpdPK, _ := cipher.GenerateKeyPair()
	otherPK, _ := cipher.GenerateKeyPair()

	conf := &visorconfig.V1{Common: &visorconfig.Common{PK: ownPK}}
	conf.Hypervisors = []cipher.PubKey{hvPK}
	conf.Pty = &visorconfig.Pty{Whitelist: []cipher.PubKey{ptyPK}}

	v := &Visor{peerWhitelist: newPeerWhitelist(conf)}

	allow := composeFeedAllowlist(v, tpdPK, true)
	for _, pk := range []cipher.PubKey{ownPK, hvPK, ptyPK, tpdPK} {
		if !contains(allow, pk) {
			t.Errorf("composed allowlist missing expected PK %s", pk)
		}
	}
	if contains(allow, otherPK) {
		t.Errorf("composed allowlist unexpectedly includes %s", otherPK)
	}
	// No duplicates.
	if len(allow) != 4 {
		t.Errorf("composed allowlist len = %d, want 4 (own+hv+pty+tpd): %v", len(allow), allow)
	}
}

// TestComposeFeedAllowlist_ConsumerAlwaysIncluded verifies the consuming
// service PK is included even when the peer whitelist has no non-zero
// entries — so gating never locks the consumer out.
func TestComposeFeedAllowlist_ConsumerAlwaysIncluded(t *testing.T) {
	tpdPK, _ := cipher.GenerateKeyPair()

	// A visor with no peer whitelist at all.
	v := &Visor{}
	allow := composeFeedAllowlist(v, tpdPK, true)
	if len(allow) != 1 || allow[0] != tpdPK {
		t.Fatalf("allowlist = %v, want exactly [tpd]", allow)
	}
}

// TestComposeFeedAllowlist_OpenWhenNothingKnown verifies the feed is left
// OPEN (nil allowlist) when both the peer whitelist is empty AND the
// consumer PK is unknown — rather than gated to an empty set that would
// reject every subscriber.
func TestComposeFeedAllowlist_OpenWhenNothingKnown(t *testing.T) {
	v := &Visor{}
	if allow := composeFeedAllowlist(v, cipher.PubKey{}, false); allow != nil {
		t.Fatalf("allowlist = %v, want nil (open) when nothing is known", allow)
	}
}

// TestComposeFeedAllowlist_PeersOnlyWhenConsumerUnknown verifies that when
// the consumer PK is unknown but the peer whitelist is populated, the feed
// is still gated to the peer whitelist (not left open).
func TestComposeFeedAllowlist_PeersOnlyWhenConsumerUnknown(t *testing.T) {
	ownPK, _ := cipher.GenerateKeyPair()
	conf := &visorconfig.V1{Common: &visorconfig.Common{PK: ownPK}}
	v := &Visor{peerWhitelist: newPeerWhitelist(conf)}

	allow := composeFeedAllowlist(v, cipher.PubKey{}, false)
	if len(allow) != 1 || allow[0] != ownPK {
		t.Fatalf("allowlist = %v, want exactly [own]", allow)
	}
}

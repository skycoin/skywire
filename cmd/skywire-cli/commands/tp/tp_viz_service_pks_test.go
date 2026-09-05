// Package clitp cmd/skywire-cli/commands/tp/tp_viz_service_pks_test.go c4-vis-cli
package clitp

import (
	"testing"

	"github.com/skycoin/skywire/pkg/tpviz"
)

const (
	tpdPK   = "02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae"
	sdPK    = "0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7"
	dmsgdPK = "022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa"
)

// The seeded set is what lets the standalone server reach the deployment
// without a registering discovery — every dmsg:// service it will dial has to
// be in it, or that service becomes unreachable rather than merely unregistered.
func TestVizServicePKsSeedsEveryDmsgService(t *testing.T) {
	pks := vizServicePKs(tpviz.Config{
		TPDURLDmsg:  "dmsg://" + tpdPK + ":80",
		SDURLDmsg:   "dmsg://" + sdPK + ":80",
		DMSGURLDmsg: "dmsg://" + dmsgdPK + ":80",
	})
	if len(pks) != 3 {
		t.Fatalf("got %d service keys, want 3", len(pks))
	}
	for _, want := range []string{tpdPK, sdPK, dmsgdPK} {
		found := false
		for _, pk := range pks {
			if pk.Hex() == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("service key %s was not seeded", want)
		}
	}
}

// A clearnet or unset URL needs no seed — it is reached over HTTP, not dmsg.
// Seeding a zero key would put a null entry in the client's cache.
func TestVizServicePKsSkipsNonDmsgAndEmpty(t *testing.T) {
	pks := vizServicePKs(tpviz.Config{
		TPDURLDmsg:  "dmsg://" + tpdPK + ":80",
		SDURLDmsg:   "",
		DMSGURLDmsg: "http://dmsgd.skywire.example/",
	})
	if len(pks) != 1 {
		t.Fatalf("got %d service keys, want only the dmsg one", len(pks))
	}
	if pks[0].Hex() != tpdPK {
		t.Errorf("seeded %s, want %s", pks[0].Hex(), tpdPK)
	}
	for _, pk := range pks {
		if pk.Null() {
			t.Error("a null public key was seeded")
		}
	}
}

// Deployments can point two services at one host. Seeding the same key twice
// is harmless but pointless, and a duplicate would misreport how many distinct
// peers the client is being told about.
func TestVizServicePKsDeduplicates(t *testing.T) {
	pks := vizServicePKs(tpviz.Config{
		TPDURLDmsg:  "dmsg://" + tpdPK + ":80",
		SDURLDmsg:   "dmsg://" + tpdPK + ":81",
		DMSGURLDmsg: "dmsg://" + dmsgdPK + ":80",
	})
	if len(pks) != 2 {
		t.Fatalf("got %d service keys, want 2 distinct", len(pks))
	}
}

// A malformed key must be dropped rather than panicking or seeding garbage —
// these come from user-supplied --tpd-url / --sd-url / --dmsg-url flags.
func TestVizServicePKsIgnoresMalformed(t *testing.T) {
	pks := vizServicePKs(tpviz.Config{
		TPDURLDmsg:  "dmsg://not-a-public-key:80",
		SDURLDmsg:   "dmsg://:80",
		DMSGURLDmsg: "dmsg://" + dmsgdPK + ":80",
	})
	if len(pks) != 1 || pks[0].Hex() != dmsgdPK {
		t.Fatalf("got %v, want only the well-formed key", pks)
	}
}

// No dmsg services configured is a legitimate clearnet-only run, and must
// yield an empty seed set rather than a slice containing a null key.
func TestVizServicePKsEmptyWhenNoDmsgServices(t *testing.T) {
	if pks := vizServicePKs(tpviz.Config{}); len(pks) != 0 {
		t.Errorf("got %v, want no service keys", pks)
	}
}

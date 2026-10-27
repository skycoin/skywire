package services

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestDmsgDiscEntries(t *testing.T) {
	t.Run("empty falls back to embedded prod servers", func(t *testing.T) {
		got := DmsgDiscEntries(nil)
		if len(got) != len(dmsg.Prod.DmsgServers) {
			t.Errorf("empty input → %d entries, want prod set (%d)", len(got), len(dmsg.Prod.DmsgServers))
		}
	})

	t.Run("non-empty copies entries and skips nils", func(t *testing.T) {
		e1 := &disc.Entry{Static: cipher.PubKey{0x02}}
		e2 := &disc.Entry{Static: cipher.PubKey{0x03}}
		got := DmsgDiscEntries([]*disc.Entry{e1, nil, e2})
		if len(got) != 2 {
			t.Fatalf("got %d entries, want 2 (nil skipped)", len(got))
		}
		if got[0].Static != e1.Static || got[1].Static != e2.Static {
			t.Errorf("entries not copied in order: %+v", got)
		}
	})
}

func TestIdentity(t *testing.T) {
	log := logging.MustGetLogger("services_test")
	pk, sk := cipher.GenerateKeyPair()

	id := (&Common{SecKey: sk}).Identity(log)
	if id.PK != pk || id.DmsgPort != dmsg.DefaultDmsgHTTPPort {
		t.Fatalf("derived identity = %v:%d, want %v:%d", id.PK, id.DmsgPort, pk, dmsg.DefaultDmsgHTTPPort)
	}
	if want := pk.Hex() + ":80"; id.DmsgAddr != want {
		t.Errorf("DmsgAddr = %q, want %q", id.DmsgAddr, want)
	}

	if id := (&Common{}).Identity(log); id.DmsgAddr != "" {
		t.Errorf("no key → DmsgAddr %q, want empty", id.DmsgAddr)
	}
}

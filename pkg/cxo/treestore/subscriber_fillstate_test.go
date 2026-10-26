package treestore

import (
	"errors"
	"testing"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// A broken fill must leave the subscriber reporting that it holds no whole
// Root, and why, even after its salvage fired the update callback. The
// tpd-metrics feed served salvaged September leaves as a clean sync for a
// day this way (2026-10-15).
func TestFillStateReportsABrokenFill(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	s := newTestSubscriber(t)
	s.feedPK = pk

	if filled, err := s.FillState(); filled || err != nil {
		t.Fatalf("new subscriber: filled=%v err=%v", filled, err)
	}

	s.handleFillingBreaks(&registry.Root{Pub: skycipher.PubKey(pk), Seq: 7}, errors.New("no connections to fill from"))
	if filled, err := s.FillState(); filled || err == nil {
		t.Fatalf("after a broken fill: filled=%v err=%v", filled, err)
	}

	s.handleRootFilled(&registry.Root{Pub: skycipher.PubKey(pk), Seq: 8})
	if filled, err := s.FillState(); !filled || err != nil {
		t.Fatalf("after a whole Root: filled=%v err=%v", filled, err)
	}
}

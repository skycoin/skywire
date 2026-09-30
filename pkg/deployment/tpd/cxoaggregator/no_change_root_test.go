package cxoaggregator

import (
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// A Root repeating the last applied tree is a "no change": skipped, except
// once per noChangeFullEvery; a new tree is always applied.
func TestRootIsRepeat(t *testing.T) {
	a := &Aggregator{}
	rep, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	root := func(b byte) *registry.Root {
		return &registry.Root{Refs: []registry.Dynamic{{Hash: skycipher.SHA256{b}}}}
	}
	t0 := time.Now()

	if a.rootIsRepeat(rep, root(1), t0) {
		t.Fatal("first Root reported as a repeat")
	}
	if !a.rootIsRepeat(rep, root(1), t0.Add(45*time.Second)) {
		t.Fatal("heartbeat Root with the same tree was not skipped")
	}
	if a.rootIsRepeat(other, root(1), t0.Add(45*time.Second)) {
		t.Fatal("another reporter's Root was skipped")
	}
	if a.rootIsRepeat(rep, root(1), t0.Add(noChangeFullEvery)) {
		t.Fatal("no full apply after noChangeFullEvery")
	}
	if a.rootIsRepeat(rep, root(2), t0.Add(noChangeFullEvery+time.Second)) {
		t.Fatal("changed tree was skipped")
	}
	a.forgetRoot(rep)
	if a.rootIsRepeat(rep, root(2), t0.Add(noChangeFullEvery+2*time.Second)) {
		t.Fatal("forgotten reporter's Root was skipped")
	}
}

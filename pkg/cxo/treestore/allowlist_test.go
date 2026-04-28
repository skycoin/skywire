package treestore

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestAllowStateOpenByDefault(t *testing.T) {
	a := newAllowState(nil)
	pk, _ := cipher.GenerateKeyPair()
	if !a.permits(pk) {
		t.Fatal("nil allowlist must permit any PK")
	}
	if a.list() != nil {
		t.Fatalf("disabled gate should report nil list, got %v", a.list())
	}
}

func TestAllowStateClosedWhenEmptyNonNil(t *testing.T) {
	a := newAllowState([]cipher.PubKey{})
	pk, _ := cipher.GenerateKeyPair()
	if a.permits(pk) {
		t.Fatal("empty non-nil allowlist must reject all PKs")
	}
}

func TestAllowStateMembership(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	pk3, _ := cipher.GenerateKeyPair()

	a := newAllowState([]cipher.PubKey{pk1, pk2})
	if !a.permits(pk1) || !a.permits(pk2) {
		t.Fatal("listed PKs must be permitted")
	}
	if a.permits(pk3) {
		t.Fatal("unlisted PK must be rejected")
	}

	got := a.list()
	if len(got) != 2 {
		t.Fatalf("list len = %d, want 2", len(got))
	}
}

func TestAllowStateReplace(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	a := newAllowState([]cipher.PubKey{pk1})
	if !a.permits(pk1) || a.permits(pk2) {
		t.Fatal("initial state wrong")
	}

	a.replace([]cipher.PubKey{pk2})
	if a.permits(pk1) {
		t.Fatal("pk1 should no longer be permitted after replace")
	}
	if !a.permits(pk2) {
		t.Fatal("pk2 should be permitted after replace")
	}

	a.replace(nil)
	if !a.permits(pk1) || !a.permits(pk2) {
		t.Fatal("nil replace must reopen the gate")
	}
}

func TestPublisherAllowlistAccessors(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	// Construct a publisher with an open gate (no allowlist set).
	p, _ := newTestPublisher(t)
	if got := p.Allowlist(); got != nil {
		t.Fatalf("default Allowlist() = %v, want nil", got)
	}
	if !p.AllowsSubscriber(pk1) {
		t.Fatal("default publisher must allow any subscriber")
	}

	// Raise the gate at runtime.
	p.SetAllowlist([]cipher.PubKey{pk1})
	if !p.AllowsSubscriber(pk1) {
		t.Fatal("pk1 should be allowed after SetAllowlist")
	}
	if p.AllowsSubscriber(pk2) {
		t.Fatal("pk2 must be rejected when not listed")
	}
	if got := p.Allowlist(); len(got) != 1 || got[0] != pk1 {
		t.Fatalf("Allowlist() = %v, want [pk1]", got)
	}

	// Drop the gate.
	p.SetAllowlist(nil)
	if !p.AllowsSubscriber(pk2) {
		t.Fatal("pk2 should be allowed after SetAllowlist(nil)")
	}
}

func TestPublisherInitialAllowlistFromConfig(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	_, sk := cipher.GenerateKeyPair()
	p, err := New(nopNode(t), sk, Config{
		SubscriberAllowlist: []cipher.PubKey{pk1},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Logf("publisher.Close: %v", err)
		}
	})

	if !p.AllowsSubscriber(pk1) {
		t.Fatal("pk1 from initial Config should be allowed")
	}
	if p.AllowsSubscriber(pk2) {
		t.Fatal("pk2 must be rejected when not in initial Config allowlist")
	}
}

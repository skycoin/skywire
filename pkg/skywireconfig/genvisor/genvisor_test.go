package genvisor

import (
	"encoding/json"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestGenerate_FreshIdentity(t *testing.T) {
	v, err := Generate(Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if v.PK.Null() {
		t.Error("PK should be derived (non-zero) when SecretKey is empty")
	}
	if v.SK.Null() {
		t.Error("SK should be generated when Options.SecretKey is empty")
	}
	if v.Dmsg == nil {
		t.Error("Dmsg config should be populated from deployment.Prod")
	}
	if v.Transport == nil {
		t.Error("Transport config should be populated")
	}
	if v.Routing == nil {
		t.Error("Routing config should be populated")
	}
	if v.Launcher == nil {
		t.Error("Launcher config should be populated")
	}
}

func TestGenerate_PinnedIdentity(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	v, err := Generate(Options{SecretKey: sk})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if v.SK != sk {
		t.Errorf("V1.SK = %v; want pinned %v", v.SK, sk)
	}
}

func TestGenerate_Hypervisor(t *testing.T) {
	v, err := Generate(Options{IsHypervisor: true})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if v.Hypervisor == nil {
		t.Fatal("Hypervisor stanza should be populated when IsHypervisor=true")
	}
	if !v.Hypervisor.Enable {
		t.Error("Hypervisor.Enable should be true when IsHypervisor=true")
	}
}

func TestGenerate_HypervisorPKs(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	v, err := Generate(Options{HypervisorPKs: []cipher.PubKey{pk1, pk2}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(v.Hypervisors) != 2 {
		t.Fatalf("V1.Hypervisors len = %d; want 2", len(v.Hypervisors))
	}
	if v.Hypervisors[0] != pk1 || v.Hypervisors[1] != pk2 {
		t.Error("V1.Hypervisors did not preserve order / PKs")
	}
}

func TestGenerate_RewardAddress(t *testing.T) {
	addr := "2jnZqqJsCMB1v4VUKk6f1PDN9EuLZQxqoqp"
	v, err := Generate(Options{RewardAddress: addr})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if v.RewardAddress != addr {
		t.Errorf("V1.RewardAddress = %q; want %q", v.RewardAddress, addr)
	}
}

func TestGenerate_IsPublic(t *testing.T) {
	v, err := Generate(Options{IsPublic: true})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !v.IsPublic {
		t.Error("V1.IsPublic = false; want true")
	}
}

// TestGenerate_JSONRoundTrip verifies the generated V1 marshals to
// JSON cleanly and re-unmarshals into a V1 without loss. Catches
// drift between the WASM-clean V1 (and its leaf-package field
// types) and the on-disk visor config shape — if a future field
// change breaks JSON round-trip, this test goes red.
func TestGenerate_JSONRoundTrip(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	original, err := Generate(Options{
		IsHypervisor:  true,
		HypervisorPKs: []cipher.PubKey{pk1},
		RewardAddress: "2jnZqqJsCMB1v4VUKk6f1PDN9EuLZQxqoqp",
		IsPublic:      true,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	buf, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var roundTripped visorconfig.V1
	if err := json.Unmarshal(buf, &roundTripped); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if roundTripped.RewardAddress != original.RewardAddress {
		t.Errorf("RewardAddress drift: %q vs %q", roundTripped.RewardAddress, original.RewardAddress)
	}
	if !roundTripped.IsPublic {
		t.Error("IsPublic dropped through round-trip")
	}
	if len(roundTripped.Hypervisors) != 1 || roundTripped.Hypervisors[0] != pk1 {
		t.Errorf("Hypervisors drift: %v vs %v", roundTripped.Hypervisors, original.Hypervisors)
	}
	if roundTripped.Dmsg == nil || roundTripped.Dmsg.Discovery == "" {
		t.Error("Dmsg.Discovery dropped through round-trip")
	}
}

func TestMustMarshalJSON(t *testing.T) {
	v, err := Generate(Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	buf := MustMarshalJSON(v)
	if len(buf) == 0 {
		t.Fatal("MustMarshalJSON returned empty")
	}
	// Should re-parse cleanly.
	var rt visorconfig.V1
	if err := json.Unmarshal(buf, &rt); err != nil {
		t.Fatalf("re-unmarshal of indented JSON: %v", err)
	}
}

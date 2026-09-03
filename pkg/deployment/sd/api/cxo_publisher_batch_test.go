// Package api — cxo_publisher_batch_test.go proves the SD services
// publisher shards to ONE leaf per service TYPE (object count O(#types),
// not O(#services)) and that a batched leaf round-trips through the
// version-framed encoding.
package api

import (
	"encoding/json"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

// newTestPub builds a publisher with only the in-memory state map wired,
// so stateSet / encodeServicesBatch can be exercised without a live
// treestore.Publisher (pub stays nil; these helpers never touch it).
func newTestPub() *ServicesCXOPublisher {
	return &ServicesCXOPublisher{state: make(map[string]map[cipher.PubKey][]byte)}
}

func mustSvc(t *testing.T, svcType string, pk cipher.PubKey) []byte {
	t.Helper()
	svc := &servicedisc.Service{
		Addr:    servicedisc.NewSWAddr(pk, 44),
		Type:    svcType,
		Version: "v1.0.0",
	}
	b, err := json.Marshal(svc)
	if err != nil {
		t.Fatalf("marshal service: %v", err)
	}
	return b
}

// TestBatchLeafCountIsPerType feeds a large service population across a
// small set of types and asserts the number of batch leaves (== types in
// state) is bounded by #types, NOT #services.
func TestBatchLeafCountIsPerType(t *testing.T) {
	p := newTestPub()

	types := []string{"vpn", "visor", "skysocks", "proxy"}
	const nServices = 600
	for i := 0; i < nServices; i++ {
		pk, _ := cipher.GenerateKeyPair()
		svcType := types[i%len(types)]
		p.stateSet(svcType, pk, mustSvc(t, svcType, pk))
	}

	if got := len(p.state); got > len(types) {
		t.Fatalf("batch leaf count = %d, want <= %d types", got, len(types))
	}
	t.Logf("%d services collapsed to %d type leaves", nServices, len(p.state))

	// Every type leaf must decode back to exactly its service set.
	total := 0
	for svcType, svcs := range p.state {
		blob := encodeServicesBatch(svcs)
		version, payload, ok := cxoutils.UnframeGzip(blob)
		if !ok || version != servicesBatchVersion {
			t.Fatalf("unframe type %s: ok=%v version=%d", svcType, ok, version)
		}
		var decoded []servicedisc.Service
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("decode type %s: %v", svcType, err)
		}
		if len(decoded) != len(svcs) {
			t.Fatalf("type %s: decoded %d services, want %d", svcType, len(decoded), len(svcs))
		}
		for i := range decoded {
			if decoded[i].Type != svcType {
				t.Fatalf("type %s: decoded service has type %q", svcType, decoded[i].Type)
			}
		}
		total += len(decoded)
	}
	if total != nServices {
		t.Fatalf("sum of decoded services = %d, want %d", total, nServices)
	}
}

// TestServicesBatchEncodeDeterministic pins that an unchanged service set
// re-encodes to identical bytes (sorted by PK), so a re-Put is a CXO wire
// no-op — the basis of the heartbeat short-circuit.
func TestServicesBatchEncodeDeterministic(t *testing.T) {
	p := newTestPub()
	for i := 0; i < 20; i++ {
		pk, _ := cipher.GenerateKeyPair()
		p.stateSet("vpn", pk, mustSvc(t, "vpn", pk))
	}
	a := encodeServicesBatch(p.state["vpn"])
	b := encodeServicesBatch(p.state["vpn"])
	if string(a) != string(b) {
		t.Fatal("encodeServicesBatch not deterministic for an unchanged set")
	}
}

// TestServicesBatchDelEmptiesType proves a type with no services left is
// dropped from state (its leaf would be Deleted, not left as a tombstone).
func TestServicesBatchDelEmptiesType(t *testing.T) {
	p := newTestPub()
	pk, _ := cipher.GenerateKeyPair()
	p.stateSet("vpn", pk, mustSvc(t, "vpn", pk))
	if len(p.state["vpn"]) != 1 {
		t.Fatalf("setup: want 1 service, got %d", len(p.state["vpn"]))
	}
	p.stateDel("vpn", pk)
	if got := len(p.state["vpn"]); got != 0 {
		t.Fatalf("after del: want 0 services, got %d", got)
	}
}

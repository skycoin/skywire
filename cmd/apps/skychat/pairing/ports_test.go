package pairing

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestComputePairPortsSymmetric(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()

	ab, err := ComputePairPorts(a, b)
	if err != nil {
		t.Fatalf("a,b: %v", err)
	}
	ba, err := ComputePairPorts(b, a)
	if err != nil {
		t.Fatalf("b,a: %v", err)
	}
	if ab != ba {
		t.Fatalf("ports must be symmetric: ab=%+v ba=%+v", ab, ba)
	}
}

func TestComputePairPortsInRange(t *testing.T) {
	for i := 0; i < 50; i++ {
		a, _ := cipher.GenerateKeyPair()
		b, _ := cipher.GenerateKeyPair()
		pp, err := ComputePairPorts(a, b)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if pp.Publisher < pubBase || pp.Publisher >= pubBase+pubSpan {
			t.Errorf("iter %d: publisher port %d out of [%d, %d)", i, pp.Publisher, pubBase, pubBase+pubSpan)
		}
		if pp.Subscriber < subBase || pp.Subscriber >= subBase+pubSpan {
			t.Errorf("iter %d: subscriber port %d out of [%d, %d)", i, pp.Subscriber, subBase, subBase+pubSpan)
		}
		if pp.Subscriber-pp.Publisher != pubSpan {
			t.Errorf("iter %d: subscriber should be publisher+%d, got publisher=%d sub=%d", i, pubSpan, pp.Publisher, pp.Subscriber)
		}
	}
}

func TestComputePairPortsAvoidsReserved(t *testing.T) {
	// Synthesize a reserved set that includes the deterministic slot
	// for a given pair, and verify publisherPort walks past it.
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()

	// First, find what the natural deterministic port would be.
	natural, err := publisherPort(a, b, map[uint16]struct{}{})
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// Now reserve that exact slot and re-compute.
	reserved := map[uint16]struct{}{natural: {}}
	walked, err := publisherPort(a, b, reserved)
	if err != nil {
		t.Fatalf("with collision: %v", err)
	}
	if walked == natural {
		t.Fatalf("expected walk past reserved port %d, still got %d", natural, walked)
	}
	// walked should be the next non-reserved slot in the span.
	if walked < pubBase || walked >= pubBase+pubSpan {
		t.Errorf("walked port %d out of pub range", walked)
	}
}

func TestComputePairPortsAvoidsRealReservedTable(t *testing.T) {
	// Cross-check that no random pair lands on a port from the real
	// reserved table. With 50 random pairs and 12 reserved ports out
	// of 50000 slots, the probability of a hit is ~1.2% per pair, so
	// across 50 iterations we expect >40% chance of at least one
	// collision IF the avoidance code didn't work — running the test
	// repeatedly with avoidance on should never report one.
	reserved := ReservedPorts()
	for i := 0; i < 200; i++ {
		a, _ := cipher.GenerateKeyPair()
		b, _ := cipher.GenerateKeyPair()
		pp, err := ComputePairPorts(a, b)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if _, hit := reserved[pp.Publisher]; hit {
			t.Errorf("iter %d: publisher port %d landed on reserved", i, pp.Publisher)
		}
		if _, hit := reserved[pp.Subscriber]; hit {
			t.Errorf("iter %d: subscriber port %d landed on reserved", i, pp.Subscriber)
		}
	}
}

func TestOrderedPair(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	loAB, hiAB := orderedPair(a, b)
	loBA, hiBA := orderedPair(b, a)
	if loAB != loBA || hiAB != hiBA {
		t.Fatal("orderedPair must be commutative")
	}
}

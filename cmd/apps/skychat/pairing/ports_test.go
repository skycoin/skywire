package pairing

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestComputePairPortSymmetric(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()

	ab, err := ComputePairPort(a, b)
	if err != nil {
		t.Fatalf("a,b: %v", err)
	}
	ba, err := ComputePairPort(b, a)
	if err != nil {
		t.Fatalf("b,a: %v", err)
	}
	if ab != ba {
		t.Fatalf("ports must be symmetric: ab=%d ba=%d", ab, ba)
	}
}

func TestComputePairPortInRange(t *testing.T) {
	for i := 0; i < 50; i++ {
		a, _ := cipher.GenerateKeyPair()
		b, _ := cipher.GenerateKeyPair()
		port, err := ComputePairPort(a, b)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if port < PortBase || port >= PortBase+PortSpan {
			t.Errorf("iter %d: port %d out of [%d, %d)", i, port, PortBase, PortBase+PortSpan)
		}
	}
}

func TestComputePairPortAvoidsReserved(t *testing.T) {
	// Synthesize a reserved set that includes the deterministic slot
	// for a given pair, and verify pairPort walks past it.
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()

	natural, err := pairPort(a, b, map[uint16]struct{}{})
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	reserved := map[uint16]struct{}{natural: {}}
	walked, err := pairPort(a, b, reserved)
	if err != nil {
		t.Fatalf("with collision: %v", err)
	}
	if walked == natural {
		t.Fatalf("expected walk past reserved port %d, still got %d", natural, walked)
	}
	if walked < PortBase || walked >= PortBase+PortSpan {
		t.Errorf("walked port %d out of pair range", walked)
	}
}

func TestComputePairPortAvoidsRealReservedTable(t *testing.T) {
	// Sanity-check that random pairs don't land on a real reserved port.
	reserved := ReservedPorts()
	for i := 0; i < 200; i++ {
		a, _ := cipher.GenerateKeyPair()
		b, _ := cipher.GenerateKeyPair()
		port, err := ComputePairPort(a, b)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if _, hit := reserved[port]; hit {
			t.Errorf("iter %d: port %d landed on reserved", i, port)
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

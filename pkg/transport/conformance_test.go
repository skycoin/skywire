// Package transport pkg/transport/conformance_test.go
package transport

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// entryWith builds a distinct Entry keyed by a remote-PK marker byte (each
// distinct remote yields a distinct deterministic transport ID).
func entryWith(remoteLast byte) *Entry {
	var a, b cipher.PubKey
	a[0], a[32] = 0x02, 0x01
	b[0], b[32] = 0x03, remoteLast
	e := MakeEntry(a, b, "stcpr", LabelAutomatic)
	return &e
}

func TestConformanceDiff(t *testing.T) {
	a := entryWith(0x0a)
	b := entryWith(0x0b)
	c := entryWith(0x0c)

	// published {a,b,c}; reflected {a,b} + one stale extra {d}.
	d := entryWith(0x0d)
	published := []*Entry{a, b, c}
	reflected := []*Entry{a, b, d}

	res := conformanceDiff(published, reflected)
	if res.Published != 3 || res.Reflected != 3 {
		t.Fatalf("counts: published=%d reflected=%d", res.Published, res.Reflected)
	}
	if res.Matched != 2 {
		t.Errorf("matched=%d, want 2", res.Matched)
	}
	if res.MissingCount != 1 || res.Missing[0] != c.ID {
		t.Errorf("missing=%v (count %d), want [%v]", res.Missing, res.MissingCount, c.ID)
	}
	if res.StaleCount != 1 || res.Stale[0] != d.ID {
		t.Errorf("stale=%v (count %d), want [%v]", res.Stale, res.StaleCount, d.ID)
	}
	if res.Ratio < 0.66 || res.Ratio > 0.67 {
		t.Errorf("ratio=%.3f, want ~0.667", res.Ratio)
	}
}

func TestConformanceDiffFullMatch(t *testing.T) {
	a := entryWith(0x0a)
	b := entryWith(0x0b)
	res := conformanceDiff([]*Entry{a, b}, []*Entry{b, a})
	if res.Ratio != 1 || res.MissingCount != 0 || res.StaleCount != 0 {
		t.Errorf("full match: ratio=%.2f missing=%d stale=%d", res.Ratio, res.MissingCount, res.StaleCount)
	}
}

func TestConformanceDiffEmptyPublished(t *testing.T) {
	// No published transports → ratio defined as 1 (nothing to reflect).
	res := conformanceDiff(nil, nil)
	if res.Ratio != 1 {
		t.Errorf("empty ratio=%.2f, want 1", res.Ratio)
	}
}

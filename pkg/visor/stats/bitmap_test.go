package stats

import (
	"strings"
	"testing"
	"time"
)

func TestSlotForTime(t *testing.T) {
	// 00:00 UTC → slot 0; 00:05 → slot 1; 23:55 → slot 287.
	cases := []struct {
		hh, mm int
		want   int
	}{
		{0, 0, 0},
		{0, 4, 0},
		{0, 5, 1},
		{0, 9, 1},
		{0, 10, 2},
		{12, 0, 144},
		{23, 55, 287},
		{23, 59, 287},
	}
	base := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	for _, c := range cases {
		got := SlotForTime(base.Add(time.Duration(c.hh)*time.Hour + time.Duration(c.mm)*time.Minute))
		if got != c.want {
			t.Errorf("SlotForTime(%02d:%02d) = %d, want %d", c.hh, c.mm, got, c.want)
		}
	}
}

func TestSetGetSlot(t *testing.T) {
	bm := make([]byte, BitmapSize)
	// MSB-first: setting slot 0 must light the high bit of byte 0.
	SetSlot(bm, 0)
	if bm[0] != 0x80 {
		t.Fatalf("slot 0 set: bm[0] = %#x, want 0x80", bm[0])
	}
	SetSlot(bm, 7)
	if bm[0] != 0x81 {
		t.Fatalf("slot 7 set: bm[0] = %#x, want 0x81", bm[0])
	}
	SetSlot(bm, 8)
	if bm[1] != 0x80 {
		t.Fatalf("slot 8 set: bm[1] = %#x, want 0x80", bm[1])
	}
	if !GetSlot(bm, 0) || !GetSlot(bm, 7) || !GetSlot(bm, 8) {
		t.Fatal("GetSlot disagrees with SetSlot")
	}
	if GetSlot(bm, 1) {
		t.Fatal("unset slot reports true")
	}
}

func TestSetSlotOutOfRange(t *testing.T) {
	bm := make([]byte, BitmapSize)
	SetSlot(bm, -1) // ignored
	SetSlot(bm, SlotsPerDay)
	for _, b := range bm {
		if b != 0 {
			t.Fatalf("out-of-range writes mutated bitmap: %x", bm)
		}
	}
}

func TestRender(t *testing.T) {
	bm := make([]byte, BitmapSize)
	SetSlot(bm, 0)
	SetSlot(bm, 144)
	SetSlot(bm, 287)
	got := Render(bm)
	if len(got) != SlotsPerDay {
		t.Fatalf("render len = %d, want %d", len(got), SlotsPerDay)
	}
	if got[0] != '.' || got[144] != '.' || got[287] != '.' {
		t.Fatal("expected dots at set slots")
	}
	// Every other position must be space.
	without := strings.ReplaceAll(got, ".", "")
	for _, c := range without {
		if c != ' ' {
			t.Fatalf("unexpected rune %q in render", c)
		}
	}
	if strings.Count(got, ".") != 3 {
		t.Fatalf("expected 3 set bits, got %d", strings.Count(got, "."))
	}
}

func TestAnd(t *testing.T) {
	a := make([]byte, BitmapSize)
	b := make([]byte, BitmapSize)
	SetSlot(a, 0)
	SetSlot(a, 100)
	SetSlot(a, 200)
	SetSlot(b, 100)
	SetSlot(b, 200)
	SetSlot(b, 287)

	out := And(a, b)
	if !GetSlot(out, 100) || !GetSlot(out, 200) {
		t.Fatal("AND should preserve slots set in both")
	}
	if GetSlot(out, 0) || GetSlot(out, 287) {
		t.Fatal("AND should clear slots set in only one")
	}

	if got := And(a, []byte{0x00}); got != nil {
		t.Fatal("AND of mismatched lengths should return nil")
	}
}

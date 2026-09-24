// Package logging pkg/logging/panics_test.go c0-com-log
package logging

import (
	"strings"
	"testing"
)

func TestPanicStatsEmptyByDefault(t *testing.T) {
	ResetPanicStats()
	count, last := PanicStats()
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
	if last != nil {
		t.Fatalf("last = %v, want nil", last)
	}
}

func TestRecoverRecordsSiteValueAndStack(t *testing.T) {
	ResetPanicStats()

	func() {
		defer Recover(nil, "unit test site")
		panic("boom")
	}()

	count, last := PanicStats()
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if len(last) != 1 {
		t.Fatalf("len(last) = %d, want 1", len(last))
	}
	e := last[0]
	if e.Site != "unit test site" {
		t.Errorf("Site = %q, want %q", e.Site, "unit test site")
	}
	if e.Value != "boom" {
		t.Errorf("Value = %q, want %q", e.Value, "boom")
	}
	// The stack must name this test, or it cannot say where the panic came
	// from — which is the whole reason the helper captures one.
	if !strings.Contains(e.Stack, "TestRecoverRecordsSiteValueAndStack") {
		t.Errorf("Stack does not name the panicking test:\n%s", e.Stack)
	}
	if e.Time.IsZero() {
		t.Error("Time is zero")
	}
}

func TestRecoverIsANoOpWithoutAPanic(t *testing.T) {
	ResetPanicStats()

	func() {
		defer Recover(nil, "quiet site")
	}()

	if count, _ := PanicStats(); count != 0 {
		t.Fatalf("count = %d, want 0 — Recover recorded something with no panic", count)
	}
}

// The ring is bounded but the count is not: that pairing is what tells a
// reader whether the entries they can see are the whole story.
func TestPanicRingIsBoundedButCountKeepsRising(t *testing.T) {
	ResetPanicStats()

	total := PanicRingSize + 5
	for i := 0; i < total; i++ {
		RecordPanic("site", i, []byte("stack"))
	}

	count, last := PanicStats()
	if count != uint64(total) {
		t.Errorf("count = %d, want %d", count, total)
	}
	if len(last) != PanicRingSize {
		t.Fatalf("len(last) = %d, want %d", len(last), PanicRingSize)
	}
	// Oldest first, and the oldest retained is the (total-PanicRingSize)th.
	if want := "5"; last[0].Value != want {
		t.Errorf("last[0].Value = %q, want %q (oldest retained)", last[0].Value, want)
	}
	if want := "12"; last[len(last)-1].Value != want {
		t.Errorf("last[-1].Value = %q, want %q (newest)", last[len(last)-1].Value, want)
	}
}

func TestRecordPanicCapturesAStackWhenGivenNone(t *testing.T) {
	ResetPanicStats()

	RecordPanic("site", "v", nil)

	_, last := PanicStats()
	if len(last) != 1 {
		t.Fatalf("len(last) = %d, want 1", len(last))
	}
	if !strings.Contains(last[0].Stack, "TestRecordPanicCapturesAStackWhenGivenNone") {
		t.Errorf("Stack was not captured:\n%s", last[0].Stack)
	}
}

//go:build !tinygo

package logging

import (
	"strings"
	"testing"
)

func TestRingBuffer_RetainsRecentAndBounds(t *testing.T) {
	rb := NewRingBuffer(16)

	// Recent content is retained verbatim while under capacity.
	if _, err := rb.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := string(rb.Bytes()); got != "hello\n" {
		t.Fatalf("got %q, want %q", got, "hello\n")
	}

	// Past capacity, oldest bytes are dropped (trimmed to a line boundary) and
	// the buffer never exceeds its cap.
	for i := 0; i < 20; i++ {
		_, _ = rb.Write([]byte("xxxxx\n")) //nolint:errcheck // 6 bytes each, far over cap
	}
	got := rb.Bytes()
	if len(got) > 16 {
		t.Fatalf("buffer exceeded cap: %d > 16", len(got))
	}
	if strings.Contains(string(got), "hello") {
		t.Fatalf("expected oldest content dropped, got %q", got)
	}
	if !strings.Contains(string(got), "xxxxx") {
		t.Fatalf("expected recent content retained, got %q", got)
	}

	// Bytes returns a copy — mutating it must not affect the buffer.
	cp := rb.Bytes()
	if len(cp) > 0 {
		cp[0] = '!'
	}
	if string(rb.Bytes()) == string(cp) && len(cp) > 0 {
		t.Fatal("Bytes() did not return a copy")
	}
}

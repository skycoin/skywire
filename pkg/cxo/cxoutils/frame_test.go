package cxoutils

import (
	"bytes"
	"testing"
)

func TestFrameGzipRoundTrip(t *testing.T) {
	payload := []byte(`[{"a":1},{"b":2}]`)
	blob := FrameGzip(7, payload)
	if blob[0] != 7 {
		t.Fatalf("version byte = %d, want 7", blob[0])
	}
	// gzip should actually compress a repetitive-ish JSON array of any size;
	// at minimum the framed blob carries the version byte + gzip stream.
	if len(blob) < 3 {
		t.Fatalf("framed blob too short: %d", len(blob))
	}
	version, got, ok := UnframeGzip(blob)
	if !ok || version != 7 {
		t.Fatalf("unframe: ok=%v version=%d", ok, version)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload round-trip mismatch: got %q want %q", got, payload)
	}
}

func TestUnframeGzipEmpty(t *testing.T) {
	if _, _, ok := UnframeGzip(nil); ok {
		t.Fatal("empty blob should report ok=false")
	}
}

func TestUnframeGzipRawPayload(t *testing.T) {
	// A non-gzipped payload behind the version byte round-trips unchanged
	// (Gunzip passes through anything lacking the gzip magic).
	raw := []byte("not-gzip")
	blob := append([]byte{9}, raw...)
	version, got, ok := UnframeGzip(blob)
	if !ok || version != 9 || !bytes.Equal(got, raw) {
		t.Fatalf("raw passthrough failed: ok=%v version=%d got=%q", ok, version, got)
	}
}

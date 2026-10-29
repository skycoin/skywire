package cxoutils

import (
	"bytes"
	"fmt"
	"testing"
)

func TestGzipRoundTrip(t *testing.T) {
	orig := []byte(`[{"t_id":"abc","edges":["x","y"],"type":"stcpr"}]`)
	gz := Gzip(orig)
	if len(gz) < 2 || gz[0] != 0x1f || gz[1] != 0x8b {
		t.Fatalf("Gzip output is not a gzip stream: %x", gz[:min(2, len(gz))])
	}
	if !bytes.Equal(Gunzip(gz), orig) {
		t.Errorf("Gunzip(Gzip(x)) != x")
	}
}

func TestGunzipPassesThroughRawJSON(t *testing.T) {
	// A raw (non-gzip) body — e.g. from a publisher that predates compression —
	// must pass through unchanged (self-describing format, skew-safe).
	raw := []byte(`{"hello":"world"}`)
	if !bytes.Equal(Gunzip(raw), raw) {
		t.Errorf("Gunzip must pass raw JSON through unchanged")
	}
}

func TestGzipEmpty(t *testing.T) {
	if Gzip(nil) != nil {
		t.Errorf("Gzip(nil) should be nil")
	}
	if Gunzip(nil) != nil {
		t.Errorf("Gunzip(nil) should be nil")
	}
}

func TestGunzipMalformedDegradesToRaw(t *testing.T) {
	// Bytes that start with the gzip magic but aren't a valid stream must not be
	// dropped — return them unchanged so the caller can try to parse as raw.
	bad := []byte{0x1f, 0x8b, 0x00, 0x01, 0x02}
	if !bytes.Equal(Gunzip(bad), bad) {
		t.Errorf("malformed gzip should degrade to the original bytes")
	}
}

// BenchmarkGunzip decompresses a 1 MB JSON-like body, the size of a mid-sized
// CXO feed leaf.
func BenchmarkGunzip(b *testing.B) {
	var raw bytes.Buffer
	for i := 0; raw.Len() < 1<<20; i++ {
		fmt.Fprintf(&raw, `{"id":"%08x-1234","edges":["02%064x"],"sent":%d},`, i, i, i*7919)
	}
	gz := Gzip(raw.Bytes())
	b.ReportAllocs()
	b.SetBytes(int64(raw.Len()))
	for b.Loop() {
		if len(Gunzip(gz)) != raw.Len() {
			b.Fatal("short")
		}
	}
}

// A pooled reader left mid-stream by a bad body still decodes the next one,
// and a forged ISIZE cannot make Gunzip allocate past its ratio cap.
func TestGunzipPooledReaderAndForgedSize(t *testing.T) {
	orig := bytes.Repeat([]byte(`{"k":"v"},`), 5000)
	gz := Gzip(orig)
	bad := append([]byte(nil), gz[:len(gz)/2]...)
	for i := 0; i < 3; i++ {
		if got := Gunzip(bad); !bytes.Equal(got, bad) {
			t.Fatal("a truncated stream must come back raw")
		}
		if !bytes.Equal(Gunzip(gz), orig) {
			t.Fatal("round trip after a bad stream")
		}
	}
	two := append(append([]byte(nil), gz...), Gzip([]byte("tail"))...)
	if got := Gunzip(two); !bytes.Equal(got, append(append([]byte(nil), orig...), "tail"...)) {
		t.Fatal("concatenated members must decode in full")
	}
	forged := append([]byte(nil), gz...)
	copy(forged[len(forged)-4:], []byte{0xff, 0xff, 0xff, 0xff})
	if h := gunzipSizeHint(forged); h > len(forged)*64 {
		t.Fatalf("hint %d for a %d byte stream", h, len(forged))
	}
}

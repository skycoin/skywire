package cxoutils

import (
	"bytes"
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

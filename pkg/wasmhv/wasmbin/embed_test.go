//go:build embedwasm

package wasmbin

import "testing"

func TestEmbeddedGet(t *testing.T) {
	if !Embedded() {
		t.Fatal("Embedded() false under -tags embedwasm")
	}
	b, err := Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// the wasm-visor binary starts with the wasm magic \0asm
	if len(b) < 4 || string(b[:4]) != "\x00asm" {
		t.Fatalf("not a wasm binary (len=%d, magic=%x)", len(b), b[:min(4, len(b))])
	}
	t.Logf("decompressed wasm-visor: %.1f MB", float64(len(b))/1024/1024)
}

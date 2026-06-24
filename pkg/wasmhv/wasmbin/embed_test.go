package wasmbin

import "testing"

// TestEmbeddedGet confirms the committed wasm-visor.wasm.gz decompresses to a
// valid wasm binary (so a default build — incl. `go install` — really carries a
// working wasm-visor).
func TestEmbeddedGet(t *testing.T) {
	if !Embedded() {
		t.Fatal("wasm-visor binary not embedded (is pkg/wasmhv/wasmbin/wasm-visor.wasm.gz committed?)")
	}
	b, err := Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// the wasm-visor binary starts with the wasm magic \0asm
	if len(b) < 4 || string(b[:4]) != "\x00asm" {
		t.Fatalf("not a wasm binary (len=%d)", len(b))
	}
	t.Logf("embedded wasm-visor: %.1f MB decompressed", float64(len(b))/1024/1024)
}

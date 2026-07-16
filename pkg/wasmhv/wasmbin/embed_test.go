package wasmbin

import (
	"testing"
)

// TestEmbeddedGet confirms the default committed wasm-visor.wasm.gz decompresses
// to a valid wasm binary (so a default build — incl. `go install` — really
// carries a working wasm-visor).
func TestEmbeddedGet(t *testing.T) {
	if !Embedded() {
		t.Fatal("wasm-visor binary not embedded (is pkg/wasmhv/wasmbin/wasmgo/wasm-visor.wasm.gz committed?)")
	}
	b, err := Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// the wasm-visor binary starts with the wasm magic \0asm
	if len(b) < 4 || string(b[:4]) != "\x00asm" {
		t.Fatalf("not a wasm binary (len=%d)", len(b))
	}
	t.Logf("default variant %q: %.1f MB decompressed", Default(), float64(len(b))/1024/1024)
}

// TestVariants confirms each embedded variant decompresses to a valid wasm and
// carries a matching wasm_exec.js loader. Under a standard-Go build this asserts
// BOTH the Go and TinyGo wasm-visors are embedded (the PWA-size requirement);
// under a TinyGo build only TinyGo is present.
func TestVariants(t *testing.T) {
	avail := Available()
	if len(avail) == 0 {
		t.Fatal("no wasm-visor variants embedded")
	}
	for _, v := range avail {
		b, err := GetVariant(v)
		if err != nil {
			t.Fatalf("GetVariant(%q): %v", v, err)
		}
		if len(b) < 4 || string(b[:4]) != "\x00asm" {
			t.Fatalf("variant %q: not a wasm binary (len=%d)", v, len(b))
		}
		if len(WasmExecJSVariant(v)) == 0 {
			t.Fatalf("variant %q: missing wasm_exec.js loader", v)
		}
		t.Logf("variant %q: %.1f MB decompressed", v, float64(len(b))/1024/1024)
	}
}

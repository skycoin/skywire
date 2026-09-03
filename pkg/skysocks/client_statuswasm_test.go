package skysocks

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
)

// TestStatusWasmResponses checks the /main.wasm and /wasm_exec.js status routes
// serve the wasm-visor "netview" blob (and its matching loader) that the GPU
// route-graph view instantiates same-origin. When a blob is embedded (the normal
// build) both are 200 with the right content types; the wasm rides gzip-encoded.
func TestStatusWasmResponses(t *testing.T) {
	v, ok := statusWasmVariant()
	if !ok {
		t.Skip("no wasm-visor blob embedded in this build")
	}
	t.Logf("serving wasm-visor variant %q", v)

	wasmResp := parseResp(t, statusWasmResponse())
	if wasmResp.StatusCode != http.StatusOK {
		t.Fatalf("/main.wasm status = %d", wasmResp.StatusCode)
	}
	if ct := wasmResp.Header.Get("Content-Type"); ct != "application/wasm" {
		t.Errorf("/main.wasm content-type = %q, want application/wasm", ct)
	}
	if ce := wasmResp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("/main.wasm content-encoding = %q, want gzip", ce)
	}

	execResp := parseResp(t, statusWasmExecResponse())
	if execResp.StatusCode != http.StatusOK {
		t.Fatalf("/wasm_exec.js status = %d", execResp.StatusCode)
	}
	if ct := execResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("/wasm_exec.js content-type = %q", ct)
	}
}

func parseResp(t *testing.T, raw []byte) *http.Response {
	t.Helper()
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(string(raw))), nil)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	return resp
}

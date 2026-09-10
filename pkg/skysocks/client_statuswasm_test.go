package skysocks

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// TestStatusWasmResponses checks the /main.wasm and /wasm_exec.js status routes
// serve the skywire command module (run in its "netview" role) and Go's loader
// that the GPU route-graph view instantiates same-origin. The loader is always
// there; the module is 200 + gzip + the execwasm stamp as ETag when one is
// embedded (the two-stage build), else 503.
func TestStatusWasmResponses(t *testing.T) {
	wasmResp := parseResp(t, statusWasmResponse())
	if !execwasm.Present() {
		if wasmResp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("/main.wasm status = %d without an embedded module, want 503", wasmResp.StatusCode)
		}
		t.Log("no skywire.wasm module embedded in this build (make embed-exec-wasm): /main.wasm is 503")
	} else {
		if wasmResp.StatusCode != http.StatusOK {
			t.Fatalf("/main.wasm status = %d", wasmResp.StatusCode)
		}
		if ct := wasmResp.Header.Get("Content-Type"); ct != "application/wasm" {
			t.Errorf("/main.wasm content-type = %q, want application/wasm", ct)
		}
		if ce := wasmResp.Header.Get("Content-Encoding"); ce != "gzip" {
			t.Errorf("/main.wasm content-encoding = %q, want gzip", ce)
		}
		if et := wasmResp.Header.Get("ETag"); et != `"`+execwasm.Stamp()+`"` {
			t.Errorf("/main.wasm etag = %q, want the execwasm stamp %q", et, execwasm.Stamp())
		}
	}

	execResp := parseResp(t, statusWasmExecResponse())
	if execResp.StatusCode != http.StatusOK {
		t.Fatalf("/wasm_exec.js status = %d", execResp.StatusCode)
	}
	if ct := execResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("/wasm_exec.js content-type = %q", ct)
	}
	body, err := io.ReadAll(execResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "this.argv=['skywire','desk-host','--role','netview']") {
		t.Error("/wasm_exec.js is not pinned to the netview role")
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

package skysocks

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/tpviz/netview"
)

// TestStatusWasmResponses checks the /main.wasm and /wasm_exec.js status routes
// serve the netview module and its loader, which the GPU route-graph view
// instantiates same-origin.
func TestStatusWasmResponses(t *testing.T) {
	var wasmRaw bytes.Buffer
	writeStatusWasmResponse(&wasmRaw)
	wasmResp := parseResp(t, wasmRaw.Bytes())
	if wasmResp.StatusCode != http.StatusOK {
		t.Fatalf("/main.wasm status = %d", wasmResp.StatusCode)
	}
	if ct := wasmResp.Header.Get("Content-Type"); ct != "application/wasm" {
		t.Errorf("/main.wasm content-type = %q, want application/wasm", ct)
	}
	if ce := wasmResp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("/main.wasm content-encoding = %q, want gzip", ce)
	}
	if et := wasmResp.Header.Get("ETag"); et != netview.ETag {
		t.Errorf("/main.wasm etag = %q, want %q", et, netview.ETag)
	}
	if body, err := io.ReadAll(wasmResp.Body); err != nil || !bytes.Equal(body, netview.WasmGz) {
		t.Errorf("/main.wasm body is not the netview module (err %v)", err)
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
	if !bytes.Equal(body, netview.ExecJS) {
		t.Error("/wasm_exec.js is not the netview loader")
	}
}

func parseResp(t *testing.T, raw []byte) *http.Response {
	t.Helper()
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), nil)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	return resp
}

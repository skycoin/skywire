// Package visorconfig pkg/visor/visorconfig/preserve_test.go
// — pins unknown-key preservation across load → modify → flush.
package visorconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// writeConf writes body to a temp file and returns its path.
func writeConf(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skywire-config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// loadJSON re-reads a flushed config as a generic tree.
func loadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("read back invalid json: %v\n%s", err, b)
	}
	return got
}

// dig walks a decoded JSON tree by key, failing the test if any hop is
// missing or not an object.
func dig(t *testing.T, tree map[string]any, keys ...string) map[string]any {
	t.Helper()
	for _, k := range keys {
		next, ok := tree[k]
		if !ok {
			t.Fatalf("key %q missing", k)
		}
		obj, ok := next.(map[string]any)
		if !ok {
			t.Fatalf("key %q is %T, want object", k, next)
		}
		tree = obj
	}
	return tree
}

// TestFlushPreservesUnknownKeys is the regression test for the data
// loss this file exists to fix: a config key the running binary has no
// field for must survive a load → modify → flush cycle, at the top
// level and at every level of nesting the visor rewrites.
//
// The nesting mirrors the real incident — hypervisor.wasm_serve.exec_wasm
// was added by a newer build, and one restart on an older binary erased
// it from disk. Here `future_setting` stands in for any such key.
func TestFlushPreservesUnknownKeys(t *testing.T) {
	path := writeConf(t, `{
	"version": "v1.3.29",
	"future_setting": "top-level",
	"cli_addr": "localhost:3435",
	"routing": {"min_hops": 1},
	"hypervisor": {
		"enable": true,
		"http_addr": ":8000",
		"future_setting": {"nested": true},
		"wasm_serve": {
			"addr": ":8009",
			"exec_wasm": "/usr/local/bin/skywire.wasm"
		}
	}
}`)

	conf, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Modify a known field, exactly as a running visor does.
	if err := conf.UpdateMinHops(3); err != nil {
		t.Fatalf("UpdateMinHops (flush): %v", err)
	}

	got := loadJSON(t, path)

	if got["future_setting"] != "top-level" {
		t.Errorf("top-level unknown key lost: %v", got["future_setting"])
	}
	hv := dig(t, got, "hypervisor")
	if nested, ok := hv["future_setting"].(map[string]any); !ok || nested["nested"] != true {
		t.Errorf("hypervisor.future_setting lost: %v", hv["future_setting"])
	}
	// wasm_serve.exec_wasm is unknown to THIS binary only if the field
	// has not landed yet; either way it must still be on disk.
	ws := dig(t, got, "hypervisor", "wasm_serve")
	if ws["exec_wasm"] != "/usr/local/bin/skywire.wasm" {
		t.Errorf("hypervisor.wasm_serve.exec_wasm lost: %v", ws["exec_wasm"])
	}
	// The modification itself must have landed, and known keys must
	// still be intact.
	if hv["http_addr"] != ":8000" {
		t.Errorf("known nested key corrupted: http_addr = %v", hv["http_addr"])
	}
	routing := dig(t, got, "routing")
	if routing["min_hops"] != float64(3) {
		t.Errorf("min_hops = %v, want 3", routing["min_hops"])
	}
}

// TestFlushDoesNotResurrectClearedKnownFields: preserving unknown keys
// must not make KNOWN keys sticky. Clearing a field the struct models
// still clears it on disk.
func TestFlushDoesNotResurrectClearedKnownFields(t *testing.T) {
	path := writeConf(t, `{
	"version": "v1.3.29",
	"reward_address": "2fF1FoQ6BAgvNzn8jVBAe6xHFHiGPYRJPZ",
	"routing": {"min_hops": 1}
}`)

	conf, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	conf.RewardAddress = "" // omitempty: drops out of the marshaled form
	if err := conf.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if got := loadJSON(t, path); got["reward_address"] != nil {
		t.Errorf("cleared known field was resurrected: %v", got["reward_address"])
	}
}

// TestFlushDoesNotResurrectRetiredKeys: the legacy "dmsgpty" key is
// consumed by a rename migration (config_compat.go). Preserving
// unknown keys must not defeat that migration by writing the retired
// key back alongside its canonical replacement.
func TestFlushDoesNotResurrectRetiredKeys(t *testing.T) {
	path := writeConf(t, `{
	"version": "v1.3.29",
	"dmsgpty": {"dmsg_port": 22, "ssh_listen": ":2022"}
}`)

	conf, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if conf.Pty == nil || conf.Pty.SshListen != ":2022" {
		t.Fatalf("legacy dmsgpty key did not migrate into V1.Pty")
	}
	if err := conf.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got := loadJSON(t, path)
	if _, ok := got["dmsgpty"]; ok {
		t.Error("retired key dmsgpty was written back, defeating the rename migration")
	}
	if _, ok := got["pty"]; !ok {
		t.Error("canonical key pty missing after flush")
	}
}

// TestFlushUnchangedWithoutUnknownKeys: a config fully modeled by this
// binary must flush to exactly the bytes json.MarshalIndent produces,
// so the merge changes nothing for the overwhelmingly common case.
func TestFlushUnchangedWithoutUnknownKeys(t *testing.T) {
	path := writeConf(t, `{"version": "v1.3.29", "cli_addr": "localhost:3435"}`)

	conf, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := conf.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	want, err := json.MarshalIndent(conf, "", "\t")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	got, err := os.ReadFile(path) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("flushed bytes differ from MarshalIndent output\n got: %s\nwant: %s", got, want)
	}
}

// TestFlushPreservesUnknownKeysInsideArrays: unknown keys inside array
// elements re-attach by index while the array keeps its length, and are
// dropped rather than misplaced when it does not.
func TestFlushPreservesUnknownKeysInsideArrays(t *testing.T) {
	const body = `{
	"version": "v1.3.29",
	"coin_nodes": [
		{"coin": "skycoin", "addr": "127.0.0.1:6420", "future_setting": 1},
		{"coin": "mdl", "addr": "127.0.0.1:6421"}
	]
}`
	path := writeConf(t, body)
	conf, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := conf.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	nodes, ok := loadJSON(t, path)["coin_nodes"].([]any)
	if !ok || len(nodes) != 2 {
		t.Fatalf("coin_nodes not an array of 2: %v", nodes)
	}
	first, ok := nodes[0].(map[string]any)
	if !ok {
		t.Fatalf("coin_nodes[0] is %T", nodes[0])
	}
	if first["future_setting"] != float64(1) {
		t.Errorf("unknown key inside array element lost: %v", first["future_setting"])
	}

	// Resized array: the residue can no longer be placed safely, so it
	// is dropped — but nothing else may break.
	path = writeConf(t, body)
	conf, err = ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	conf.CoinNodes = conf.CoinNodes[:1]
	if err := conf.Flush(); err != nil {
		t.Fatalf("Flush after resize: %v", err)
	}
	if nodes, ok := loadJSON(t, path)["coin_nodes"].([]any); !ok || len(nodes) != 1 {
		t.Fatalf("coin_nodes after resize: %v", nodes)
	}
}

// TestFlushWithNoPriorFile: a config synthesized in memory (config gen,
// STDIN) has no residue and must still write cleanly to a path that
// does not exist yet.
func TestFlushWithNoPriorFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new-config.json")
	_, sk := cipher.GenerateKeyPair()
	cc, err := NewCommon(nil, path, &sk)
	if err != nil {
		t.Fatalf("NewCommon: %v", err)
	}
	conf := MakeBaseConfig(cc, false, true, nil, nil)
	if err := conf.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := ReadFile(path); err != nil {
		t.Fatalf("flushed config does not load back: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o640 {
		t.Errorf("config file mode = %o, want 640 (it holds the secret key)", perm)
	}
}

// TestCaptureUnknownOnInvalidJSON: a malformed document must not panic
// the walk; it simply yields no residue.
func TestCaptureUnknownOnInvalidJSON(t *testing.T) {
	for _, body := range []string{"", "{not json", "[]", "null", `"scalar"`} {
		if res := captureUnknown([]byte(body), reflect.TypeOf(V1{})); res != nil {
			t.Errorf("captureUnknown(%q) = %#v, want nil", body, res)
		}
	}
}

// TestFlushPreservesUnknownKeysAfterCommonSwap: `skywire cli config
// update` re-points the config at a fresh Common before flushing (see
// initUpdate in cmd/skywire-cli/commands/config/update.go), so the
// preserved keys cannot be carried on the loaded struct — they have to
// come from the file being overwritten. Pins that.
func TestFlushPreservesUnknownKeysAfterCommonSwap(t *testing.T) {
	path := writeConf(t, `{
	"version": "v1.3.29",
	"future_setting": "top-level",
	"routing": {"min_hops": 1},
	"hypervisor": {"enable": true, "wasm_serve": {"addr": ":8009", "exec_wasm": "/x.wasm"}}
}`)

	conf, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	cc, err := NewCommon(nil, path, &conf.SK)
	if err != nil {
		t.Fatalf("NewCommon: %v", err)
	}
	conf.Common = cc
	if err := conf.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got := loadJSON(t, path)
	if got["future_setting"] != "top-level" {
		t.Errorf("top-level unknown key lost after Common swap: %v", got["future_setting"])
	}
	if ws := dig(t, got, "hypervisor", "wasm_serve"); ws["exec_wasm"] != "/x.wasm" {
		t.Errorf("nested unknown key lost after Common swap: %v", ws["exec_wasm"])
	}
}

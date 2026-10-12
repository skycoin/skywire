// Package execwasm pkg/wasmhv/execwasm/execwasm.go c3-wasm-embed
//
// The full skywire command module for GOOS=js: the desk's `skywire` command
// and the tab visor. It is not embedded in the native binary. It ships beside
// it as skywire.wasm.gz with a manifest, skywire.wasm.json, both written by
// scripts/wasm-module.sh, and a visor serves that copy at /skywire.wasm and
// refreshes it when the desk is opened (pkg/visor/wasmmodule.go).
package execwasm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// The module and its manifest, as installed beside the binary.
const (
	ModuleName   = "skywire.wasm.gz"
	ManifestName = "skywire.wasm.json"
)

// Manifest describes one build of the module.
type Manifest struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

// DefaultPath is the module installed beside the running binary, "" when the
// executable cannot be located.
func DefaultPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return filepath.Join(filepath.Dir(exe), ModuleName)
}

// ManifestPath is the manifest beside the module at path.
func ManifestPath(path string) string {
	return filepath.Join(filepath.Dir(path), ManifestName)
}

// ReadManifest reads the manifest beside the module at path.
func ReadManifest(path string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(ManifestPath(path)) //nolint:gosec // beside the module the operator installed
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("%s: %w", ManifestPath(path), err)
	}
	return m, nil
}

// Stamp fingerprints the module at path for ETags and the desk's served
// version: the manifest's sha256 when it has one, else the file's size and
// mtime, so a module rebuilt in place by a developer still changes it. It is
// "" when there is no module.
func Stamp(path string) string {
	if path == "" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if m, err := ReadManifest(path); err == nil && len(m.SHA256) >= 16 && m.Size == fi.Size() {
		return m.SHA256[:16]
	}
	h := sha256.Sum256(fmt.Appendf(nil, "%d:%d", fi.Size(), fi.ModTime().UnixNano()))
	return hex.EncodeToString(h[:])[:16]
}

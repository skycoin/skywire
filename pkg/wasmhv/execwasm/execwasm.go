// Package execwasm pkg/wasmhv/execwasm/execwasm.go c3-wasm-embed
//
// The full skywire command module for GOOS=js: the desk's `skywire` command
// and the tab visor. It is not embedded in the native binary, and packages do
// not ship it. It lives beside the binary as skywire.wasm.gz with a manifest,
// skywire.wasm.json, both written by scripts/wasm-module.sh, and is fetched
// when the desk is first opened and refreshed after (pkg/visor/wasmmodule.go).
package execwasm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

// DefaultPath is where the module is kept: beside the running binary, or in
// the user's cache directory when it is not there already and the binary's
// directory cannot be written, as for a packaged binary run by a user. It is
// "" when the executable cannot be located.
func DefaultPath() string {
	defaultOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		if p, err := filepath.EvalSymlinks(exe); err == nil {
			exe = p
		}
		cache, _ := os.UserCacheDir() //nolint:errcheck // no cache dir leaves the binary's
		defaultPath = pathIn(filepath.Dir(exe), cache)
	})
	return defaultPath
}

var (
	defaultOnce sync.Once
	defaultPath string
)

// pathIn picks the module beside the binary in dir when it exists there or dir
// is writable, else the one under cache, which is created.
func pathIn(dir, cache string) string {
	beside := filepath.Join(dir, ModuleName)
	if _, err := os.Stat(beside); err == nil || cache == "" || writable(dir) {
		return beside
	}
	cached := filepath.Join(cache, "skywire", ModuleName)
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil { //nolint:gosec // the module is public
		return beside
	}
	return cached
}

// writable reports whether a file can be created in dir.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".skywire-write-")
	if err != nil {
		return false
	}
	f.Close()           //nolint:errcheck,gosec
	os.Remove(f.Name()) //nolint:errcheck,gosec
	return true
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

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
	"strings"
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
		defaultPath = pathIn(filepath.Dir(exe), cacheDirs()...)
	})
	return defaultPath
}

var (
	defaultOnce sync.Once
	defaultPath string
)

// cacheDirs are where the module can go when the binary's directory cannot
// be written. A systemd service often has no $HOME, so os.UserCacheDir fails
// there; the unit's own CacheDirectory= or StateDirectory= is used instead.
func cacheDirs() []string {
	var dirs []string
	if d, err := os.UserCacheDir(); err == nil && d != "" {
		dirs = append(dirs, filepath.Join(d, "skywire"))
	}
	for _, env := range []string{"CACHE_DIRECTORY", "STATE_DIRECTORY"} {
		if d, _, _ := strings.Cut(os.Getenv(env), ":"); d != "" {
			dirs = append(dirs, d)
		}
	}
	return append(dirs, filepath.Join(os.TempDir(), "skywire"))
}

// pathIn picks the module beside the binary in dir when it exists there or dir
// is writable, else the one in the first of caches that can be written.
func pathIn(dir string, caches ...string) string {
	beside := filepath.Join(dir, ModuleName)
	if _, err := os.Stat(beside); err == nil || writable(dir) {
		return beside
	}
	for _, c := range caches {
		if os.MkdirAll(c, 0o755) == nil && writable(c) { //nolint:gosec // the module is public
			return filepath.Join(c, ModuleName)
		}
	}
	return beside
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

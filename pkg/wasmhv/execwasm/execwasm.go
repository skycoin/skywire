// Package execwasm pkg/wasmhv/execwasm/execwasm.go c3-wasm-embed
//
// The full skywire command module for GOOS=js, embedded in the native binary
// by the two-stage build: the module is built first (GOOS=js GOARCH=wasm go
// build .), gzipped into blob/skywire.wasm.gz (gitignored), then the native
// binary is built with it inside. It is the desk's `skywire` command and the
// tab visor; hv serve and the native hypervisor serve it at /skywire.wasm.
//
// A source build without that step embeds only blob/README: Present() is
// false and the servers fall back to an on-disk module (ExecWasmPath) or to
// the legacy page. The gzipped bytes are what is embedded and what is served
// when the client accepts gzip (every browser does); the module is never held
// inflated in memory.
package execwasm

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"sync"
)

//go:embed blob
var blobFS embed.FS

const blobName = "blob/skywire.wasm.gz"

var (
	once  sync.Once
	gz    []byte
	stamp string
)

func load() {
	once.Do(func() {
		b, err := blobFS.ReadFile(blobName)
		if err != nil || len(b) == 0 {
			return
		}
		gz = b
		sum := sha256.Sum256(b)
		stamp = hex.EncodeToString(sum[:])[:16]
	})
}

// Present reports whether a module is embedded.
func Present() bool {
	load()
	return len(gz) > 0
}

// Gz returns the embedded module, gzipped, or nil.
func Gz() []byte {
	load()
	return gz
}

// Stamp is a short content fingerprint of the embedded module, "" when none:
// it changes with every build, so pages polling the served version reload.
func Stamp() string {
	load()
	return stamp
}

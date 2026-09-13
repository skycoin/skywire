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
	"encoding/hex"
	"sync"
)

const blobName = "blob/skywire.wasm.gz"

var (
	once     sync.Once
	gz       []byte
	stamp    string
	revision string
)

func load() {
	once.Do(func() {
		b := embeddedGz()
		if len(b) == 0 {
			return
		}
		gz = b
		sum := sha256.Sum256(b)
		stamp = hex.EncodeToString(sum[:])[:16]
		revision = embeddedRevision()
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

// revisionName is written beside the module by `make embed-exec-wasm`: the
// vcs.revision the module was built from, lifted at build time because the
// module is never inflated in memory and scanning 176 MB for it at startup
// would defeat that.
const revisionName = "blob/revision.txt"

// Revision is the commit the embedded module was built from, or "" when that
// is not recorded (a source build with no staged module, or one staged before
// this was written).
//
// It exists because the two failure modes here are silent. `go build .` embeds
// whatever blob/ already holds rather than rebuilding it, so a native binary
// happily serves a module many commits older than itself; and the served page
// reports the module's own version, which simply looks like a different number
// rather than a stale one. Comparing this to buildinfo.Commit() is the only
// cheap way to notice.
func Revision() string {
	load()
	return revision
}

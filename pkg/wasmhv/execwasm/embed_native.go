//go:build !js && !mobile

//go:embedbuild -gzip -strip -tags=withoutsystray,withoutgotop -fallback blob/skywire.wasm.gz GOOS=js GOARCH=wasm github.com/skycoin/skywire

// Package execwasm pkg/wasmhv/execwasm/embed_native.go c3-wasm-embed
package execwasm

import (
	"embed"
	"io/fs"
	"strings"
)

// Only the NATIVE binary embeds the module. The module is itself the root
// binary built for GOOS=js and compiles this package too; embedding here
// under js would put a staged blob inside the module it is a copy of.
//
//go:embed blob
var blobFS embed.FS

// blobName is the staged module inside blobFS.
const blobName = "blob/skywire.wasm.gz"

// revisionName is written beside the module by `make embed-exec-wasm`: the
// vcs.revision the module was built from, lifted at build time because the
// module is never inflated in memory and scanning 176 MB for it at startup
// would defeat that.
const revisionName = "blob/revision.txt"

// embeddedOpen opens the staged module for reading. The bytes live in the
// binary's read-only data, which the kernel maps from the executable file:
// shared between processes and evictable under pressure. Reading through this
// handle copies only into the caller's buffer, where embed.FS.ReadFile copies
// the whole ~37 MB onto the Go heap and keeps it resident for the life of the
// process — on every visor, whether or not anything ever asks for the module.
func embeddedOpen() (fs.File, error) { return blobFS.Open(blobName) }

// embeddedSize is the staged module's size, 0 when none is staged. It is a
// directory-entry lookup: nothing is read.
func embeddedSize() int64 {
	fi, err := fs.Stat(blobFS, blobName)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// embeddedRevision reads the commit recorded beside the module by
// `make embed-exec-wasm`, and the Stamp of the module it was recorded for when
// a second line holds one. It lives here, beside blobFS, because the js build
// has no blob directory and shared code that touches blobFS does not compile
// for GOOS=js.
func embeddedRevision() (rev, stamp string) {
	b, err := blobFS.ReadFile(revisionName)
	if err != nil {
		return "", ""
	}
	rev, stamp, _ = strings.Cut(string(b), "\n")
	return strings.TrimSpace(rev), strings.TrimSpace(stamp)
}

//go:build !js && !mobile

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
// `make embed-exec-wasm`. It lives here, beside blobFS, for the same reason
// embeddedOpen does: the js build has no blob directory, and shared code that
// touches blobFS directly does not compile for GOOS=js. This one is a 40-byte
// file, so reading it whole costs nothing.
func embeddedRevision() string {
	b, err := blobFS.ReadFile(revisionName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

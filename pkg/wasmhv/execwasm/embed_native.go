//go:build !js && !mobile

// Package execwasm pkg/wasmhv/execwasm/embed_native.go c3-wasm-embed
package execwasm

import (
	"embed"
	"strings"
)

// Only the NATIVE binary embeds the module. The module is itself the root
// binary built for GOOS=js and compiles this package too; embedding here
// under js would put a staged blob inside the module it is a copy of.
//
//go:embed blob
var blobFS embed.FS

func embeddedGz() []byte {
	b, err := blobFS.ReadFile(blobName)
	if err != nil {
		return nil
	}
	return b
}

// embeddedRevision reads the commit recorded beside the module by
// `make embed-exec-wasm`. It lives here, beside blobFS, for the same reason
// embeddedGz does: the js build has no blob directory, and shared code that
// touches blobFS directly does not compile for GOOS=js.
func embeddedRevision() string {
	b, err := blobFS.ReadFile(revisionName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

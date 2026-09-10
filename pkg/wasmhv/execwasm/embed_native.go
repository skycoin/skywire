//go:build !js

// Package execwasm pkg/wasmhv/execwasm/embed_native.go c3-wasm-embed
package execwasm

import "embed"

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

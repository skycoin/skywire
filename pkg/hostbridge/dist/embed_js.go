//go:build js

// Package dist pkg/hostbridge/dist/embed_js.go c3-vis-wasm
package dist

import "embed"

//go:embed *.gz
var files embed.FS

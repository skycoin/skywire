//go:build !js

// Package dist pkg/hostbridge/dist/embed_other.go c3-vis-wasm
package dist

import "embed"

// files is empty, because only a browser visor hands out the bridges.
var files embed.FS

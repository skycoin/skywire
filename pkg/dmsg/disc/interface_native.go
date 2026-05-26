//go:build !js

// Package disc pkg/dmsg/disc/interface_native.go
//
// Package-wide `json` codec (jsoniter.ConfigFastest). Build-tag-
// gated off the WASM path: jsoniter pulls encoding/json which
// in turn drags the reflect runtime helpers TinyGo's stdlib
// doesn't ship. Under js the disc package compiles without a
// json codec because nothing in the build graph reaches
// Sign / VerifySignature / client.go / testing.go (the only
// callers).
package disc

import (
	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigFastest

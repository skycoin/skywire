//go:build js && wasm

// Package cligot cmd/skywire-cli/commands/got/roots_js.go
package cligot

import "github.com/skycoin/skywire/pkg/wasmhv/cabundle"

// useBundledRoots gives got's TLS a root store in a browser tab, which has no
// system pool: without it every https fetch failed "certificate signed by
// unknown authority".
func useBundledRoots() { cabundle.InstallFallback() }

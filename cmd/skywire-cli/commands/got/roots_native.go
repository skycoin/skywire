//go:build !(js && wasm)

// Package cligot cmd/skywire-cli/commands/got/roots_native.go
package cligot

// useBundledRoots is a no-op natively: the platform verifier has the roots.
func useBundledRoots() {}

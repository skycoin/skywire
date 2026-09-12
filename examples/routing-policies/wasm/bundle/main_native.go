//go:build !tinygo

// The wasi build's real main lives in abi_tinygo.go; this stub keeps the
// package a buildable `main` for native go vet and the constraint tests
// (main_test.go), which exercise the decide helpers without the ABI.
package main

func main() {}

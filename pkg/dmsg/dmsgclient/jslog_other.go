//go:build !(js && wasm)

// Package dmsgclient pkg/dmsg/dmsgclient/jslog_other.go c1-net-dmsg
package dmsgclient

// jslog is a no-op off the browser target (see jslog_js.go).
func jslog(string) {}

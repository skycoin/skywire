//go:build !(js && wasm)

package dmsgclient

// jslog is a no-op off the browser target (see jslog_js.go).
func jslog(string) {}

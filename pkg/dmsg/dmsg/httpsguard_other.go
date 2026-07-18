//go:build !(js && wasm)

// Package dmsg pkg/dmsg/dmsg/httpsguard_other.go c1-net-dmsg
package dmsg

// insecureWSBlocked is browser-only mixed-content protection. On native there is
// no page origin, so a plain ws:// dial is never blocked here.
func insecureWSBlocked(string) bool { return false }

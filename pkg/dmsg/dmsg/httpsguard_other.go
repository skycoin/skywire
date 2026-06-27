//go:build !(js && wasm)

package dmsg

// insecureWSBlocked is browser-only mixed-content protection. On native there is
// no page origin, so a plain ws:// dial is never blocked here.
func insecureWSBlocked(string) bool { return false }

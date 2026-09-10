// Package deskhost pkg/wasmhv/deskhost/sameorigin.go c3-vis-wasm
// Untagged on purpose, like vnetaddr.go: pure string work with no syscall/js,
// so the rule it encodes — which URLs the browser role renders UNSANDBOXED —
// is pinned by a test that actually runs in CI.
package deskhost

import "strings"

// sameOriginSrc claims u for NATIVE rendering when it is a page of the origin
// serving this module. It is the DirectLoader of the "browser" role, which is
// the role the native hypervisor page loads netscrape in: there the server
// behind the page IS the visor, so every same-origin page is the visor's own
// UI and may render as an ordinary document. installDesk's loader deliberately
// claims only the vnet spellings, because the desk page may be hosted by a docs
// site whose other pages are not the visor's; that reasoning does not hold on a
// page the visor serves itself.
//
// Returns u unchanged: netscrape puts it on the iframe as src and keeps it in
// the address bar.
func sameOriginSrc(origin, u string) (string, bool) {
	if origin == "" || u == "" {
		return "", false
	}
	if u == origin || strings.HasPrefix(u, origin+"/") || strings.HasPrefix(u, origin+"?") || strings.HasPrefix(u, origin+"#") {
		return u, true
	}
	return "", false
}

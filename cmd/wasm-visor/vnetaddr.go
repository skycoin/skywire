// Package main cmd/wasm-visor/vnetaddr.go c3-vis-wasm
// Untagged on purpose: vnetTarget is pure string/URL work with no syscall/js
// in it, and a js-only test never runs in CI. The rule it encodes — which
// spellings get rendered UNSANDBOXED out of this origin — is worth a test
// that actually executes.

package main

import (
	"net/url"
	"strconv"
	"strings"
)

// vnetTarget parses the spellings that name the visor's own loopback and
// returns its port and path, or "" when the URL names something else. It
// mirrors desk-boot.js's vnetPort(), which resolves the same names for the
// transcoding fetch path — the two must agree or a tab renders one way when
// addressed "vnet:8001" and another when addressed "8001.vnet".
//
//	vnet:<port>       canonical
//	<port>.vnet
//	localhost:<port>  127.0.0.1:<port>, [::1]:<port>
func vnetTarget(raw string) (port, path string) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", ""
	}
	path = u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	// A fragment is never sent to a server; the browser applies it to the
	// document the src loads, so it has to ride along on the src.
	if u.Fragment != "" {
		path += "#" + u.EscapedFragment()
	}
	host := strings.ToLower(u.Hostname())
	if p := u.Port(); p != "" {
		switch host {
		case "vnet", "localhost", "127.0.0.1", "::1":
			if _, err := strconv.Atoi(p); err == nil {
				return p, path
			}
		}
		return "", ""
	}
	if rest, ok := strings.CutSuffix(host, ".vnet"); ok {
		if _, err := strconv.Atoi(rest); err == nil {
			return rest, path
		}
	}
	return "", ""
}

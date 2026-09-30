// Package deskhost pkg/wasmhv/deskhost/browseorigin.go c3-vis-wasm
// Untagged on purpose, like sameorigin.go: pure string work with no
// syscall/js, so the rules it encodes — which URLs earn a real isolated
// origin, and what canonical string names the target — are pinned by tests
// that actually run in CI.
//
// These strings are NOT free to change. A browse origin is the first 20 base32
// characters of SHA-256 over the canonical target (realorigin's idFor, and ID()
// in its Go half). Change how a target is spelled and every site moves to a new
// origin, losing the cookies and storage it had there. The spellings below are
// the ones the retired browse.js engine used, kept verbatim for that reason.
package deskhost

import "strings"

// b32 is RFC 4648 base32, lowercase — the alphabet cipher.PubKey.DNSLabel uses.
const b32 = "abcdefghijklmnopqrstuvwxyz234567"

// pkDNSLabel encodes a 66-hex public key as its 53-character base32 DNS label.
// Hex is too long for a DNS label at 66 characters; base32 fits under the 63
// limit, which is what lets one wildcard certificate cover a PK host.
func pkDNSLabel(hexPK string) string {
	if !isHexPK(hexPK) {
		return hexPK
	}
	var out strings.Builder
	bits, val := 0, 0
	for i := 0; i+1 < len(hexPK); i += 2 {
		b := hexVal(hexPK[i])<<4 | hexVal(hexPK[i+1])
		val = val<<8 | b
		bits += 8
		for bits >= 5 {
			out.WriteByte(b32[(val>>(bits-5))&31])
			bits -= 5
		}
	}
	if bits > 0 {
		out.WriteByte(b32[(val<<(5-bits))&31])
	}
	return out.String()
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 0
}

// isHexPK reports whether s is a 66-character hex public key.
func isHexPK(s string) bool {
	if len(s) != 66 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if hexDigit(s[i]) {
			continue
		}
		return false
	}
	return true
}

func hexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// normResolverHost canonicalizes a dmsg/skynet resolver host: it strips a
// trailing .dmsg/.skynet, base32-encodes any 66-hex label, and re-appends the
// network. Aliases, already-base32 labels and name-vhosts pass through.
//
// So "<hex>.dmsg", "<base32>.dmsg" and "magnetosphere.net.<hex>.dmsg" all
// settle on one stable spelling — which is what makes a site keep its origin,
// and therefore its storage, however the reader happened to type it.
func normResolverHost(host, network string) string {
	h := strings.TrimSpace(host)
	for _, suf := range []string{".dmsg", ".skynet"} {
		if len(h) >= len(suf) && strings.EqualFold(h[len(h)-len(suf):], suf) {
			h = h[:len(h)-len(suf)]
			break
		}
	}
	parts := strings.Split(h, ".")
	for i, l := range parts {
		parts[i] = pkDNSLabel(l)
	}
	return strings.Join(parts, ".") + "." + network
}

// canonicalMeshTarget is the string a mesh browse origin is the hash of. The
// "<net>|<host>" shape is realorigin's key, not a display form.
func canonicalMeshTarget(network, host string) string { return network + "|" + host }

// meshOriginFor decides whether a URL host earns a real isolated origin, and
// on which network. It claims a mesh address and nothing else.
//
// Deliberately NOT claimed:
//   - a virtual-loopback address (vnet:<port>, <port>.vnet, localhost,
//     127.0.0.1). Those live in THIS page's vnet port table, which a browse
//     origin's service worker cannot reach — it is a different origin with a
//     different worker. DirectLoader claims them for native same-origin
//     rendering instead, and it runs first.
//   - clearnet, which clearnetOriginFor claims under its own key.
func meshOriginFor(host string) (network, resolverHost string, ok bool) {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" || isLoopbackHost(h) {
		return "", "", false
	}
	switch {
	case strings.HasSuffix(h, ".skysocks"):
		// The visor's own status pages, answered in-process by its resolving
		// proxy. A real origin gives them what the sandboxed transcoder could
		// not: their scripts' fetches and live WebSocket reach the page's
		// server. "local", not "skysocks": that descriptor net means clearnet
		// to browse-transport.js.
		return "local", h, true
	case strings.HasSuffix(h, ".skynet"):
		network = "skynet"
	case strings.HasSuffix(h, ".dmsg"), isHexPK(h):
		network = "dmsg"
	default:
		return "", "", false
	}
	return network, normResolverHost(h, network), true
}

// isLoopbackHost reports the spellings of this page's own vnet, per
// desk-boot's vnetPort: vnet, <port>.vnet, localhost, 127.0.0.1, ::1.
func isLoopbackHost(h string) bool {
	if strings.HasSuffix(h, ".vnet") {
		return true
	}
	switch h {
	case "vnet", "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return false
}

// clearnetOriginFor decides whether a clearnet URL earns a real isolated
// origin. It is the retired engine's rule, recovered from browse.js (#3809,
// deleted in #4477): every http or https URL that is not a mesh or loopback
// address loads at its own origin. The site is still fetched through the
// browser's proxy — only the origin its frame runs in changes.
//
// scheme is URL.protocol ("https:"), host URL.hostname, and origin URL.origin,
// which is the base the page is rebased onto and the one spelling that must
// not drift: the canonical target is "skysocks|" + origin, as the engine had
// it, so a site keeps the origin and storage it had there. .skysocks names a
// page the visor serves itself, never clearnet.
func clearnetOriginFor(scheme, host, origin string) (base string, ok bool) {
	h := strings.ToLower(strings.TrimSpace(host))
	if scheme != "http:" && scheme != "https:" {
		return "", false
	}
	if h == "" || isLoopbackHost(h) || strings.HasSuffix(h, ".skysocks") {
		return "", false
	}
	if _, _, mesh := meshOriginFor(h); mesh {
		return "", false
	}
	if origin == "" || origin == "null" {
		return "", false
	}
	return origin, true
}

// canonicalClearnetTarget is the string a clearnet browse origin is the hash
// of: "skysocks|<origin>", the retired engine's key.
func canonicalClearnetTarget(base string) string { return "skysocks|" + base }

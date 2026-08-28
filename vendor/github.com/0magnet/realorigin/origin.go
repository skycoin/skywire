// Package realorigin serves untrusted remote content at a genuine, isolated
// browser origin whose network layer is a service worker, while the credentials
// that fetch it stay on a separate origin behind a capability.
//
// The problem it solves is a certificate problem before it is a network one.
// Rendering content at a real origin over HTTPS needs a certificate for that
// origin's hostname, and issuing one per site exhausts CA rate limits, so one
// wildcard has to cover every site. But a wildcard certificate matches exactly
// one label and only the left-most one (RFC 6125 section 6.4.3, and the
// CA/Browser Forum Baseline Requirements): *.*.example is not a certificate any
// CA issues or any browser accepts. So the target cannot be encoded into the
// hostname — a subdomain, or a name long enough to crowd the 63-character label
// limit, has nowhere to go.
//
// The way out is to stop naming the target at all. A browse origin is a short,
// stable hash of it:
//
//	id = base32(sha256(canonical))[:20]
//	B  = https://<id>.<suffix>
//
// One wildcard then covers every site whatever the target looks like; the hash
// is deterministic, so a site keeps its cookies and storage across sessions; and
// because the frame knows only its own id, it supplies a path and never a host,
// which is what stops one browse origin asking for another's content.
package realorigin

import (
	"crypto/sha256"
	"encoding/base32"
	"strings"
)

// b32 is RFC 4648 base32 in lower case, which is what a DNS label can carry.
var b32 = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// IDLen is the number of base32 characters in a browse-origin label: 20
// characters, so 100 bits of the digest, truncated to leave room under the
// 63-character DNS label limit.
const IDLen = 20

// ID returns the browse-origin label for a canonical target string.
//
// The caller decides what canonical means for its own address space and is
// responsible for making equivalent addresses produce the same string — two
// spellings of one target that canonicalize differently get two origins, and so
// two separate cookie jars. The JavaScript half computes this identically.
func ID(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	s := b32.EncodeToString(sum[:])
	return s[:IDLen]
}

// Host returns the full browse-origin hostname for a canonical target under
// suffix, e.g. "k3f9….mesh.localhost". A missing leading dot on the suffix is
// supplied.
func Host(canonical, suffix string) string {
	return ID(canonical) + normalizeSuffix(suffix)
}

// IDFromHost recovers the label from a browse-origin hostname, and reports
// whether host was in fact a browse origin under suffix. A port is not accepted
// here: strip it first.
func IDFromHost(host, suffix string) (string, bool) {
	suffix = normalizeSuffix(suffix)
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	id := host[:len(host)-len(suffix)]
	// One label only. Anything deeper is outside a single-level wildcard, so it
	// could never have had a certificate and is not ours.
	if id == "" || strings.Contains(id, ".") {
		return "", false
	}
	return id, true
}

// normalizeSuffix guarantees the leading dot. Without it a caller's "mesh.local"
// would match "notmesh.local", which is a different registrable domain.
func normalizeSuffix(suffix string) string {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return ""
	}
	if !strings.HasPrefix(suffix, ".") {
		return "." + suffix
	}
	return suffix
}

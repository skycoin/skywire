// Package visor pkg/visor/browseorigins.go
// Untagged, dependency-free string work, so the rule it encodes is covered by
// a test that runs in CI: which app origins a browse origin will accept
// content from. Getting it wrong does not fail loudly — the browse frame just
// waits on its interstitial until the handshake times out.
package visor

import (
	"fmt"
	"net/url"
	"strings"
)

// normalizeVOrigins validates the browse_v_origin setting and returns it in
// the form the bootstrap shell parses.
//
// The setting names the app origins a browse origin will be fed content by,
// and it is the only thing deciding who may inject into that origin:
//
//	"https://app.example"                   one app, exact
//	"https://a.example, https://b.example"  several, each exact
//	"*"                                     any parent
//
// "*" is for a browse domain shared between apps. A browse origin serves only
// a static shell and worker and never any content, and third-party storage
// partitioning keys storage by (top-level site, frame origin), so an
// uninvited page cannot reach a visitor's real browsing state — but it can
// serve its own content from a subdomain of yours, so open is a decision
// rather than a default.
func normalizeVOrigins(raw string) (string, error) {
	var out []string
	open := false
	for _, part := range strings.Split(raw, ",") {
		o := strings.TrimSpace(part)
		switch {
		case o == "":
			continue
		case o == "*":
			open = true
			continue
		}
		u, err := url.Parse(o)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return "", fmt.Errorf("browse_v_origin: %q is not an origin (want scheme://host[:port], or *)", o)
		}
		if u.Path != "" && u.Path != "/" {
			return "", fmt.Errorf("browse_v_origin: %q has a path; an origin is scheme://host[:port]", o)
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return "", fmt.Errorf("browse_v_origin: %q has a query or fragment; an origin is scheme://host[:port]", o)
		}
		origin := u.Scheme + "://" + u.Host
		if !hasOrigin(out, origin) {
			out = append(out, origin)
		}
	}
	if open {
		// One "*" settles it; listing origins beside it would read as a
		// restriction that is not being applied.
		return "*", nil
	}
	if len(out) == 0 {
		return "", fmt.Errorf("browse_v_origin: no origin given")
	}
	return strings.Join(out, ","), nil
}

func hasOrigin(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

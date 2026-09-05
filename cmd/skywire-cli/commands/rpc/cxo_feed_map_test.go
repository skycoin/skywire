// Package clirpc cxo_feed_map_test.go
package clirpc

import (
	"testing"

	"github.com/skycoin/skywire/deployment"
)

// TestCXOFeedForURLStats locks in the tpd-stats rows. The two
// aggregates are the only TPD endpoints a chart needs, so the mapping
// has to route them to CXO — and has to NOT route the parameterized
// variants the publisher does not write, which would otherwise be
// answered with numbers computed over a different set.
func TestCXOFeedForURLStats(t *testing.T) {
	tpd := deployment.Prod.TransportDiscovery
	tpdDmsg := deployment.Prod.TransportDiscoveryDmsg

	cases := []struct {
		url       string
		wantFeed  string
		wantPath  string
		wantMatch bool
	}{
		{tpd + "/all-transports/stats", "tpd-stats", "network", true},
		{tpdDmsg + "/all-transports/stats", "tpd-stats", "network", true},
		{tpd + "/version", "tpd-stats", "versions", true},
		{tpdDmsg + "/version", "tpd-stats", "versions", true},
		// The endpoint's defaults, spelled out explicitly, still match.
		{tpd + "/version?on=all", "tpd-stats", "versions", true},
		{tpd + "/version?on=none", "tpd-stats", "versions", true},

		// Variants the publisher does not write must fall through to the
		// HTTP chain rather than be served a mismatched body.
		{tpd + "/all-transports/stats?selfTransports=hide", "", "", false},
		{tpd + "/version?on=true", "", "", false},
		{tpd + "/version?on=false", "", "", false},

		// The per-key rollup is deliberately not on this feed.
		{tpd + "/all-transports/per-key-stats", "", "", false},

		// And the sibling bulk endpoint keeps its own feed.
		{tpd + "/all-transports", "tpd-all-transports", "without-self", true},
	}

	for _, c := range cases {
		feed, path, ok := cxoFeedForURL(c.url)
		if ok != c.wantMatch || feed != c.wantFeed || path != c.wantPath {
			t.Errorf("cxoFeedForURL(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.url, feed, path, ok, c.wantFeed, c.wantPath, c.wantMatch)
		}
	}
}

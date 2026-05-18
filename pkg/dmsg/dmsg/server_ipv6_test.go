// Package dmsg — pkg/dmsg/dmsg/server_ipv6_test.go
//
// Coverage for the Phase 2a additions: dmsg.Server's optional IPv6
// advertised-address surface. Verifies SetAdvertisedAddrV6 /
// AdvertisedAddrV6 round-trip in isolation (no networking) and that
// the default zero-value behavior preserves single-stack v4 contract
// for callers that never touch v6.
package dmsg

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg/metrics"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	dc := disc.NewMock(0)
	return NewServer(pk, sk, dc, nil, metrics.NewEmpty())
}

// TestServer_AdvertisedAddrV6_EmptyByDefault verifies a fresh Server
// reports an empty v6 advertised address — the pre-#1525 single-stack
// default. The legacy v4 AdvertisedAddr getter blocks on addrDone, so
// we don't call it here; AdvertisedAddrV6 must NOT block (v6 is
// optional, see the doc on the getter).
func TestServer_AdvertisedAddrV6_EmptyByDefault(t *testing.T) {
	s := newTestServer(t)
	assert.Empty(t, s.AdvertisedAddrV6(),
		"Server with no SetAdvertisedAddrV6 call must report empty (v4-only contract)")
}

// TestServer_SetAdvertisedAddrV6_RoundTrip verifies the setter is
// captured by the getter and that a non-empty value survives. Mirrors
// the existing SetAdvertisedAddr contract minus the one-shot constraint
// (v6 advertisement is independent, so we don't gate it on addrDone).
func TestServer_SetAdvertisedAddrV6_RoundTrip(t *testing.T) {
	s := newTestServer(t)
	s.SetAdvertisedAddrV6("[2001:db8::1]:8081")
	assert.Equal(t, "[2001:db8::1]:8081", s.AdvertisedAddrV6())
}

// TestServer_SetAdvertisedAddrV6_OverwriteAllowed verifies that, unlike
// v4 SetAdvertisedAddr's "set once" contract (enforced by closing
// addrDone exactly once), SetAdvertisedAddrV6 can be called multiple
// times. This matters for operator workflows where the v6 endpoint is
// only known after dmsg-server inspects an external network event
// (e.g. a router advertisement arriving post-Serve).
func TestServer_SetAdvertisedAddrV6_OverwriteAllowed(t *testing.T) {
	s := newTestServer(t)
	s.SetAdvertisedAddrV6("[2001:db8::1]:8081")
	s.SetAdvertisedAddrV6("[2001:db8::2]:8081")
	assert.Equal(t, "[2001:db8::2]:8081", s.AdvertisedAddrV6())
}

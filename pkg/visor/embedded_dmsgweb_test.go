package visor

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsgspec "github.com/skycoin/skywire/pkg/dmsgc/spec"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func dmsgURL(t *testing.T) (string, string) {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return fmt.Sprintf("dmsg://%s:80", pk.Hex()), pk.Hex()
}

// TestServiceAliasMap asserts the canonical acronyms map to the configured
// service PKs, that the *Dmsg field is preferred, and that plain-HTTP / unset
// services are omitted (not aliased to a zero PK).
func TestServiceAliasMap(t *testing.T) {
	tpdURL, tpdPK := dmsgURL(t)
	arURL, arPK := dmsgURL(t)
	rfURL, rfPK := dmsgURL(t)
	sdURL, sdPK := dmsgURL(t)
	dmsgdURL, dmsgdPK := dmsgURL(t)
	rewardURL, rewardPK := dmsgURL(t)

	conf := &visorconfig.V1{
		Dmsg: &dmsgspec.DmsgConfig{DiscoveryDmsg: dmsgdURL},
		Transport: &visorconfig.Transport{
			DiscoveryDmsg:       tpdURL,
			AddressResolverDmsg: arURL,
			// AddressResolver (plain) left empty on purpose
		},
		Routing:          &visorconfig.Routing{RouteFinderDmsg: rfURL},
		Launcher:         &visorconfig.Launcher{ServiceDiscDmsg: sdURL},
		UptimeTracker:    &visorconfig.UptimeTracker{Addr: "http://ut.skycoin.com"}, // HTTP only → omitted
		RewardSystemDmsg: rewardURL,
	}

	m := serviceAliasMap(conf)

	assert.Equal(t, dmsgdPK, m["dmsgd"].Hex())
	assert.Equal(t, tpdPK, m["tpd"].Hex())
	assert.Equal(t, arPK, m["ar"].Hex())
	assert.Equal(t, rfPK, m["rf"].Hex())
	assert.Equal(t, sdPK, m["sd"].Hex())
	assert.Equal(t, rewardPK, m["rewards"].Hex())

	_, hasUT := m["ut"]
	assert.False(t, hasUT, "HTTP-only uptime tracker must not be aliased")
	assert.Len(t, m, 6, "exactly the 6 resolvable services")
}

// TestServiceAliasMapSetupNodes covers the PK-list setup nodes: indexed aliases
// plus a bare alias pointing at the first, and null-PK skipping.
func TestServiceAliasMapSetupNodes(t *testing.T) {
	rsn0, _ := cipher.GenerateKeyPair()
	rsn1, _ := cipher.GenerateKeyPair()
	tsn0, _ := cipher.GenerateKeyPair()

	conf := &visorconfig.V1{
		Routing: &visorconfig.Routing{
			RouteSetupNodes: []cipher.PubKey{rsn0, rsn1},
		},
		Transport: &visorconfig.Transport{
			TransportSetupPKs:     []cipher.PubKey{tsn0},
			UserTransportSetupPKs: []cipher.PubKey{{}}, // null PK must be skipped
		},
	}

	m := serviceAliasMap(conf)

	assert.Equal(t, rsn0.Hex(), m["rsn"].Hex(), "bare rsn points at the first")
	assert.Equal(t, rsn0.Hex(), m["rsn0"].Hex())
	assert.Equal(t, rsn1.Hex(), m["rsn1"].Hex())
	assert.Equal(t, tsn0.Hex(), m["tsn"].Hex())
	assert.Equal(t, tsn0.Hex(), m["tsn0"].Hex())

	_, hasNull := m["tsn1"]
	assert.False(t, hasNull, "null setup-node PK must not be aliased")
}

// TestDmsgServerAliases covers dmsg-server aliasing (dmsg0.., sorted by PK for
// stable indices) and the matching direct-dial PK set, including null-PK skip.
func TestDmsgServerAliases(t *testing.T) {
	s0, _ := cipher.GenerateKeyPair()
	s1, _ := cipher.GenerateKeyPair()

	servers := []*dmsgdisc.Entry{
		{Static: s0},
		{Static: s1},
		{Static: cipher.PubKey{}}, // null PK must be skipped
	}

	aliases, set := dmsgServerAliases(servers)

	// Indices are assigned in PK-sorted order, so derive the expectation.
	lo, hi := s0, s1
	if hi.Hex() < lo.Hex() {
		lo, hi = hi, lo
	}
	assert.Equal(t, lo.Hex(), aliases["dmsg0"].Hex())
	assert.Equal(t, hi.Hex(), aliases["dmsg1"].Hex())
	assert.Len(t, aliases, 2)

	assert.Contains(t, set, s0)
	assert.Contains(t, set, s1)
	assert.NotContains(t, set, cipher.PubKey{}, "null PK must not be in the direct-dial set")
	assert.Len(t, set, 2)
}

// TestServiceAliasMapNil guards the nil-config and empty paths.
func TestServiceAliasMapNil(t *testing.T) {
	assert.Empty(t, serviceAliasMap(nil))
	assert.Empty(t, serviceAliasMap(&visorconfig.V1{}))
	a, s := dmsgServerAliases(nil)
	assert.Empty(t, a)
	assert.Empty(t, s)
}

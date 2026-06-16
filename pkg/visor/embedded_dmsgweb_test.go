package visor

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgspec "github.com/skycoin/skywire/pkg/dmsgc/spec"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func dmsgURL(t *testing.T) (string, string) {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return fmt.Sprintf("dmsg://%s:80", pk.Hex()), pk.Hex()
}

// TestParseDmsgURLPK covers the dmsg-URL → PK extraction and the non-dmsg /
// malformed rejections.
func TestParseDmsgURLPK(t *testing.T) {
	raw, hexPK := dmsgURL(t)

	pk, ok := parseDmsgURLPK(raw)
	require.True(t, ok, "valid dmsg:// URL must parse")
	assert.Equal(t, hexPK, pk.Hex())

	for _, bad := range []string{
		"",                           // unset
		"http://transport.discovery", // plain HTTP — no dmsg PK
		"https://sd.skycoin.com",
		"dmsg://not-a-pubkey:80", // dmsg scheme but bad PK
		"dmsg://:80",             // empty host
	} {
		_, ok := parseDmsgURLPK(bad)
		assert.False(t, ok, "must reject %q", bad)
	}
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

	conf := &visorconfig.V1{
		Dmsg: &dmsgspec.DmsgConfig{DiscoveryDmsg: dmsgdURL},
		Transport: &visorconfig.Transport{
			DiscoveryDmsg:       tpdURL,
			AddressResolverDmsg: arURL,
			// AddressResolver (plain) left empty on purpose
		},
		Routing:       &visorconfig.Routing{RouteFinderDmsg: rfURL},
		Launcher:      &visorconfig.Launcher{ServiceDiscDmsg: sdURL},
		UptimeTracker: &visorconfig.UptimeTracker{Addr: "http://ut.skycoin.com"}, // HTTP only → omitted
	}

	m := serviceAliasMap(conf)

	assert.Equal(t, dmsgdPK, m["dmsgd"].Hex())
	assert.Equal(t, tpdPK, m["tpd"].Hex())
	assert.Equal(t, arPK, m["ar"].Hex())
	assert.Equal(t, rfPK, m["rf"].Hex())
	assert.Equal(t, sdPK, m["sd"].Hex())

	_, hasUT := m["ut"]
	assert.False(t, hasUT, "HTTP-only uptime tracker must not be aliased")
	assert.Len(t, m, 5, "exactly the 5 resolvable services")
}

// TestServiceAliasMapNil guards the nil-config and empty-services paths.
func TestServiceAliasMapNil(t *testing.T) {
	assert.Empty(t, serviceAliasMap(nil))
	assert.Empty(t, serviceAliasMap(&visorconfig.V1{}))
}

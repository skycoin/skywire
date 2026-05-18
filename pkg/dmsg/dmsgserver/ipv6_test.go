package dmsgserver_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsgserver"
)

// TestConfig_PublicAddressV6_FoldsToDeployments verifies that a
// top-level PublicAddressV6 is folded into each NormalizedDeployment's
// AdvertisedAddressV6 (mirror of the existing v4 fold logic). This is
// the path the cmd/dmsg/dmsg-server start command relies on to surface
// a single-config v6 advertisement.
func TestConfig_PublicAddressV6_FoldsToDeployments(t *testing.T) {
	c := dmsgserver.Config{
		Discovery:       "https://disc.example.com",
		PublicAddress:   "203.0.113.5:8081",
		PublicAddressV6: "[2001:db8::1]:8081",
	}
	ds := c.NormalizedDeployments()
	require.Len(t, ds, 1)
	assert.Equal(t, "203.0.113.5:8081", ds[0].AdvertisedAddress)
	assert.Equal(t, "[2001:db8::1]:8081", ds[0].AdvertisedAddressV6)
}

// TestConfig_PublicAddressV6_PerDeploymentOverride verifies the same
// fold semantics as v4: a non-empty per-deployment AdvertisedAddressV6
// wins over the top-level PublicAddressV6, identical to the existing
// AdvertisedAddress / PublicAddress relationship.
func TestConfig_PublicAddressV6_PerDeploymentOverride(t *testing.T) {
	rawJSON := `{
        "dmsg": {
            "discovery": "https://disc-local.example.com",
            "advertised_address": "10.0.0.5:8081",
            "advertised_address_v6": "[fd00::5]:8081"
        },
        "public_address": "203.0.113.5:8081",
        "public_address_v6": "[2001:db8::1]:8081"
    }`
	var c dmsgserver.Config
	require.NoError(t, json.Unmarshal([]byte(rawJSON), &c))
	ds := c.NormalizedDeployments()
	require.Len(t, ds, 1)
	// Per-deployment values win on BOTH families — confirms the v6 fold
	// follows the exact same precedence as v4.
	assert.Equal(t, "10.0.0.5:8081", ds[0].AdvertisedAddress)
	assert.Equal(t, "[fd00::5]:8081", ds[0].AdvertisedAddressV6)
}

// TestConfig_NoPublicAddressV6_BackwardCompat verifies a v4-only Config
// (no PublicAddressV6) yields v4-only deployments with
// AdvertisedAddressV6 stays empty. omitempty in JSON means the
// disc.Server.AddressV6 field ultimately gets elided from the entry,
// preserving the pre-#1525 single-stack wire format.
func TestConfig_NoPublicAddressV6_BackwardCompat(t *testing.T) {
	c := dmsgserver.Config{
		Discovery:     "https://disc.example.com",
		PublicAddress: "203.0.113.5:8081",
	}
	ds := c.NormalizedDeployments()
	require.Len(t, ds, 1)
	assert.Equal(t, "203.0.113.5:8081", ds[0].AdvertisedAddress)
	assert.Empty(t, ds[0].AdvertisedAddressV6, "v4-only config must leave v6 advertisement empty")
}

// TestConfig_OnlyV6_FoldsCorrectly is the symmetric edge case to
// TestConfig_NoPublicAddressV6_BackwardCompat: a config that declares
// ONLY a v6 endpoint (rare today, more common once v6 deployments
// become common) still produces a usable Deployment. The v4 side is
// left empty; the dmsg.Server's updateServerEntryOnEndpoint skips
// publishing when addr == "" (existing behavior).
func TestConfig_OnlyV6_FoldsCorrectly(t *testing.T) {
	c := dmsgserver.Config{
		Discovery:       "https://disc.example.com",
		PublicAddressV6: "[2001:db8::1]:8081",
	}
	ds := c.NormalizedDeployments()
	require.Len(t, ds, 1)
	assert.Empty(t, ds[0].AdvertisedAddress)
	assert.Equal(t, "[2001:db8::1]:8081", ds[0].AdvertisedAddressV6)
}

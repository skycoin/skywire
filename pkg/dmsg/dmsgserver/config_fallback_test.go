// Package dmsgserver pkg/dmsg/dmsgserver/config_fallback_test.go
package dmsgserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
)

// A config with no dmsg / discovery / servers block falls back to the embedded
// deployment, so a dmsg server needs no `config gen` step to track deployment
// changes — the binary carries the current discovery + servers.
func TestNormalizedDeployments_EmbeddedFallback(t *testing.T) {
	cfg := &Config{PublicAddress: "1.2.3.4:8081"}

	deps := cfg.NormalizedDeployments()
	require.Len(t, deps, 1)
	assert.Equal(t, deployment.Prod.DmsgDiscovery, deps[0].Discovery)
	assert.Equal(t, deployment.Prod.DmsgDiscoveryDmsg, deps[0].DiscoveryDmsg)
	assert.NotEmpty(t, deps[0].Servers, "fallback should carry the embedded dmsg servers")
	assert.Equal(t, "1.2.3.4:8081", deps[0].AdvertisedAddress,
		"top-level PublicAddress folds into AdvertisedAddress")
}

// An explicit discovery field stays authoritative — the embedded fallback is
// NOT used when the config specifies its own deployment.
func TestNormalizedDeployments_ExplicitAuthoritative(t *testing.T) {
	cfg := &Config{Discovery: "http://my-disc.example"}

	deps := cfg.NormalizedDeployments()
	require.Len(t, deps, 1)
	assert.Equal(t, "http://my-disc.example", deps[0].Discovery)
	assert.NotEqual(t, deployment.Prod.DmsgDiscovery, deps[0].Discovery)
	assert.Empty(t, deps[0].Servers, "an explicit block carries only what it specifies")
}

// A deployment that specifies its discovery_dmsg but omits `servers` borrows the
// embedded server set for the matching embedded deployment (by discovery PK),
// while keeping its own per-deployment knobs like sessions_count. This lets an
// operator drop the hand-maintained servers list yet stay strictly dmsg-only.
func TestNormalizedDeployments_EmptyServersBorrowsEmbedded(t *testing.T) {
	cfg := &Config{Dmsg: DmsgConfig{Deployments: []Deployment{{
		DiscoveryDmsg: deployment.Prod.DmsgDiscoveryDmsg,
		SessionsCount: 2,
	}}}}

	deps := cfg.NormalizedDeployments()
	require.Len(t, deps, 1)
	assert.Equal(t, deployment.Prod.DmsgDiscoveryDmsg, deps[0].DiscoveryDmsg)
	assert.Equal(t, 2, deps[0].SessionsCount, "per-deployment knobs are preserved")
	assert.NotEmpty(t, deps[0].Servers, "empty servers + known discovery → embedded servers")
	assert.Equal(t, deployment.Prod.ToDiscEntries(), deps[0].Servers)
}

// Empty servers with an UNRECOGNIZED (but valid) discovery PK stays empty — the
// embedded set is never grafted onto a deployment it doesn't belong to.
func TestNormalizedDeployments_EmptyServersUnknownDiscoveryStaysEmpty(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	cfg := &Config{Dmsg: DmsgConfig{Deployments: []Deployment{{
		DiscoveryDmsg: "dmsg://" + pk.Hex() + ":80",
	}}}}

	deps := cfg.NormalizedDeployments()
	require.Len(t, deps, 1)
	assert.Empty(t, deps[0].Servers, "unknown discovery must not borrow embedded servers")
}

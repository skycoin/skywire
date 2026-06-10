// Package dmsgserver pkg/dmsg/dmsgserver/config_fallback_test.go
package dmsgserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/deployment"
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

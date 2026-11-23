//go:build !(js && wasm) && !tinygo

package rewards

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	rewardsserver "github.com/skycoin/skywire/pkg/deployment/rewards/server"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/services/tpd"
)

type neighborsByType map[string]services.Service

func (n neighborsByType) NeighborByType(svcType string) services.Service { return n[svcType] }

// The reward service reads transport discovery in its process when one runs
// there, and the transport-discovery service is a reader of that kind.
func TestRewardsFindsTransportDiscovery(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	svc, err := factory(block(t, map[string]any{"secret_key": sk.Hex(), "work_dir": t.TempDir()}), nil)
	require.NoError(t, err)
	s := svc.(*service)
	require.Nil(t, s.transportDiscovery(), "no neighbors yet")

	tpdCfg, err := tpd.ParseBlock([]byte(`{"type":"transport-discovery"}`))
	require.NoError(t, err)
	tpdSvc := tpd.New(tpdCfg, nil)
	require.Implements(t, (*rewardsserver.TransportDiscoveryLocal)(nil), tpdSvc)

	services.NeighborsSetFor(s, neighborsByType{})
	require.Nil(t, s.transportDiscovery(), "no transport discovery here")
	services.NeighborsSetFor(s, neighborsByType{transportDiscoveryType: tpdSvc})
	require.Same(t, tpdSvc, s.transportDiscovery())
	require.Equal(t, tpd.Type, transportDiscoveryType)

	// Not started: it says so, and has no feed to read.
	_, err = s.transportDiscovery().TransportMetricsRead(t.Context(), tpdstore.MetricsQuery{Days: 1})
	require.Error(t, err)
	require.Empty(t, s.transportDiscovery().MetricsDaysRead())
}

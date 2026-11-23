// Package rewardsserver pkg/deployment/rewards/server/tpdlocal.go c3-app-rewards
//
// When transport discovery runs in the same process as the reward service,
// as the transport-discovery and rewards blocks of one visor do, the reward
// system reads TPD's records directly instead of over dmsg from its own
// visor. Without it everything goes over dmsg and CXO as before.
package rewardsserver

import (
	"context"

	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// TransportDiscoveryLocal reads transport discovery's records in this
// process. pkg/services/tpd implements it.
type TransportDiscoveryLocal interface {
	// TransportMetricsRead returns what GET /metrics answers for query.
	TransportMetricsRead(ctx context.Context, query tpdstore.MetricsQuery) ([]tpdstore.TransportMetric, error)
	// MetricsDaysRead returns the dates of the metrics feed's day leaves,
	// newest first.
	MetricsDaysRead() []string
	// MetricsDayPartsRead returns the parts of one day leaf of the metrics
	// feed, the bytes a subscriber receives.
	MetricsDayPartsRead(date string) ([][]byte, bool)
}

// TransportDiscoveryLookup returns transport discovery in this process, or
// nil when it does not run here. It is asked on each use, since a service
// under its own key can be restarted.
type TransportDiscoveryLookup func() TransportDiscoveryLocal

// local returns transport discovery in this process, or nil.
func (l TransportDiscoveryLookup) local() TransportDiscoveryLocal {
	if l == nil {
		return nil
	}
	return l()
}

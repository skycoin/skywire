// Package tpdiscmetrics pkg/transport-discovery/metrics/metrics.go c4-net-discovery
package tpdiscmetrics

import (
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// Metrics collects metrics for metrics tracking system.
type Metrics interface {
	SetTPCounts(tpCounts map[types.Type]int)
}

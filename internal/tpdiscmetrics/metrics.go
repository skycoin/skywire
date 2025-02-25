package tpdiscmetrics

import (
	"github.com/skycoin/skywire/pkg/transport/network"
)

// Metrics collects metrics for metrics tracking system.
type Metrics interface {
	SetTPCounts(tpCounts map[network.Type]int)
}

package tpdiscmetrics

import (
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// Metrics collects metrics for metrics tracking system.
type Metrics interface {
	SetTPCounts(tpCounts map[types.Type]int)
}

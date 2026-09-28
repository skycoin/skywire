// Package setupmetrics pkg/router/setupmetrics/metrics.go c2-net-routing
package setupmetrics

import (
	"github.com/skycoin/skywire/pkg/routing"
)

// Metrics collects metrics in prometheus format.
type Metrics interface {
	RecordRequest() func(*routing.EdgeRules, *error)
	RecordRoute() func(*error)
}

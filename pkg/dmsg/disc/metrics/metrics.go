// Package metrics pkg/dmsg/disc/metrics/metrics.go c1-net-dmsg
package metrics

// Metrics collects metrics for metrics tracking system.
type Metrics interface {
	SetClientsCount(val int64)
	SetServersCount(val int64)
}

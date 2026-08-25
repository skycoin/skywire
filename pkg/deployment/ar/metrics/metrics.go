// Package armetrics pkg/deployment/ar/metrics/metrics.go c4-net-discovery
package armetrics

// Metrics collects metrics for metrics tracking system.
type Metrics interface {
	SetClientsCount(val int64)
}

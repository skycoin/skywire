// Package utmetrics pkg/deployment/ut/metrics/metrics.go c4-net-discovery
package utmetrics

// Metrics collects metrics for metrics tracking system.
type Metrics interface {
	SetEntriesCount(val int64)
}

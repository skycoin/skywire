// Package utmetrics pkg/uptime-tracker/metrics/metrics.go c4-net-discovery
package utmetrics

// Metrics collects metrics for metrics tracking system.
type Metrics interface {
	SetEntriesCount(val int64)
}

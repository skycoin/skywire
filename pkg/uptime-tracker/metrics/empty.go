// Package utmetrics pkg/uptime-tracker/metrics/empty.go c4-net-discovery
package utmetrics

// NewEmpty constructs new empty metrics.
func NewEmpty() Empty {
	return Empty{}
}

// Empty implements Metrics, but does nothing.
type Empty struct{}

// SetEntriesCount implements `Metrics`.
func (Empty) SetEntriesCount(_ int64) {}

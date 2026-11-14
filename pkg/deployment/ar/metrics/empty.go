// Package armetrics pkg/deployment/ar/metrics/empty.go c3-net-discovery
package armetrics

// NewEmpty constructs new empty metrics.
func NewEmpty() Empty {
	return Empty{}
}

// Empty implements Metrics, but does nothing.
type Empty struct{}

// SetClientsCount implements `Metrics`.
func (Empty) SetClientsCount(_ int64) {}

// Package armetrics pkg/address-resolver/metrics/empty.go c4-net-discovery
package armetrics

// NewEmpty constructs new empty metrics.
func NewEmpty() Empty {
	return Empty{}
}

// Empty implements Metrics, but does nothing.
type Empty struct{}

// SetClientsCount implements `Metrics`.
func (Empty) SetClientsCount(_ int64) {}

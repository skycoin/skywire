// Package sdmetrics internal/sdmetrics/empty.go
package sdmetrics

// NewEmpty constructs new empty metrics.
func NewEmpty() Empty {
	return Empty{}
}

// Empty implements Metrics, but does nothing.
type Empty struct{}

// SetServiceTypesCount implements `Metrics`.
func (Empty) SetServiceTypesCount(_ uint64) {}

// SetServicesRegByTypeCount implements `Metrics`.
func (Empty) SetServicesRegByTypeCount(_ uint64) {}

// SetServiceTypeVPNCount implements `Metrics`.
func (Empty) SetServiceTypeVPNCount(_ uint64) {}

// SetServiceTypeVisorCount implements `Metrics`.
func (Empty) SetServiceTypeVisorCount(_ uint64) {}

// SetServiceTypeSkysocksCount implements `Metrics`.
func (Empty) SetServiceTypeSkysocksCount(_ uint64) {}

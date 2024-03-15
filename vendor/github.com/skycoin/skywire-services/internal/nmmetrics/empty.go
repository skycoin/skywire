// Package nmmetrics internal/nmmetrics/empty.go
package nmmetrics

// NewEmpty constructs new empty metrics.
func NewEmpty() Empty {
	return Empty{}
}

// Empty implements Metrics, but does nothing.
type Empty struct{}

// SetTotalVpnServerCount implements `Metrics`.
func (Empty) SetTotalVpnServerCount(_ int64) {}

// SetTotalVisorCount implements `Metrics`.
func (Empty) SetTotalVisorCount(_ int64) {}

// SetTpCount implements `Metrics`.
func (Empty) SetTpCount(_ int64, _ int64) {}

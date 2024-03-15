// Package armetrics internal/armetrics/empty.go
package armetrics

// NewEmpty constructs new empty metrics.
func NewEmpty() Empty {
	return Empty{}
}

// Empty implements Metrics, but does nothing.
type Empty struct{}

// SetClientsCount implements `Metrics`.
func (Empty) SetClientsCount(_ int64) {}

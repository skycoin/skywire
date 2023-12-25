// Package discmetrics internal/discmetrics/empty.go
package discmetrics

// NewEmpty constructs new empty metrics.
func NewEmpty() Empty {
	return Empty{}
}

// Empty implements Metrics, but does nothing.
type Empty struct{}

// SetClientsCount implements `Metrics`.
func (Empty) SetClientsCount(_ int64) {}

// SetServersCount implements `Metrics`.
func (Empty) SetServersCount(_ int64) {}

// Package utmetrics internal/utmetrics/empty.go
package utmetrics

// NewEmpty constructs new empty metrics.
func NewEmpty() Empty {
	return Empty{}
}

// Empty implements Metrics, but does nothing.
type Empty struct{}

// SetEntriesCount implements `Metrics`.
func (Empty) SetEntriesCount(_ int64) {}

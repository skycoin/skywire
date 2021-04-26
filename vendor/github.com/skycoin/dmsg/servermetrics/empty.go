package servermetrics

import (
	"net/http"
)

// NewEmpty constructs new empty metrics.
func NewEmpty() Empty {
	return Empty{}
}

// Empty implements Metrics, but does nothing.
type Empty struct{}

// RecordSession implements `Metrics`.
func (Empty) RecordSession(_ DeltaType) {}

// RecordStream implements `Metrics`.
func (Empty) RecordStream(_ DeltaType) {}

// HandleDisc implements `Metrics`.
func (Empty) HandleDisc(next http.Handler) http.HandlerFunc { return next.ServeHTTP }

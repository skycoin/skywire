package servermetrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
)

// NewEmpty implements Metrics, but does nothing.
func NewEmpty() Metrics {
	return empty{}
}

type empty struct{}

func (empty) Collectors() []prometheus.Collector            { return nil }
func (empty) RecordSession(_ DeltaType)                     {}
func (empty) RecordStream(_ DeltaType)                      {}
func (empty) HandleDisc(next http.Handler) http.HandlerFunc { return next.ServeHTTP }

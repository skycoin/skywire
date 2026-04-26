// Package metricsutil pkg/metricsutil/requests_in_flight_count_middleware.go
package metricsutil

import (
	"net/http"
)

// RequestsInFlightCountMiddleware is a middleware to track current requests-in-flight count.
type RequestsInFlightCountMiddleware struct {
	reqsInFlightGauge *VictoriaMetricsIntGaugeWrapper
}

// NewRequestsInFlightCountMiddleware constructs `RequestsInFlightCountMiddleware`.
func NewRequestsInFlightCountMiddleware() *RequestsInFlightCountMiddleware {
	return &RequestsInFlightCountMiddleware{
		reqsInFlightGauge: NewVictoriaMetricsIntGauge("vm_request_ongoing_count"),
	}
}

// Handle adds to the requests count during request serving.
func (m *RequestsInFlightCountMiddleware) Handle(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		m.reqsInFlightGauge.Inc()
		defer m.reqsInFlightGauge.Dec()

		next.ServeHTTP(w, r)
	}

	return http.HandlerFunc(fn)
}

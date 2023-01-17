// Package metricsutil pkg/metricsutil/request_duration_middleware.go
package metricsutil

import (
	"fmt"
	"net/http"
	"time"

	"github.com/VictoriaMetrics/metrics"
)

// RequestDurationMiddleware is a request duration tracking middleware.
func RequestDurationMiddleware(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		srw := NewStatusResponseWriter(w)

		reqStart := time.Now()
		next.ServeHTTP(srw, r)
		reqDuration := time.Since(reqStart)

		hName := fmt.Sprintf("vm_request_duration{code=\"%d\", method=\"%s\"}", srw.StatusCode(), r.Method)
		metrics.GetOrCreateHistogram(hName).Update(reqDuration.Seconds())
	}

	return http.HandlerFunc(fn)
}

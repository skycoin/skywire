//go:build tinygo

package metrics

import (
	"io"
	"runtime"
)

// writeGoMetrics is a minimal stand-in for the native go_metrics.go under
// TinyGo, whose runtime ships neither the full runtime.MemStats struct
// (BuckHashSys, GCCPUFraction, NumGC, PauseNs, …) nor the runtime/metrics
// and runtime.ThreadCreateProfile APIs the native collector reads. A browser
// visor has no meaningful Go-GC/thread telemetry to export anyway, so we emit
// just the goroutine count and let WriteProcessMetrics keep working.
func writeGoMetrics(w io.Writer) {
	WriteGaugeUint64(w, "go_goroutines", uint64(runtime.NumGoroutine()))
}

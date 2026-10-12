//go:build gopus_silk_trace

package silk

const ltpAnalysisTraceEnabled = true

var silkLTPAnalysisTraceHook func(*Encoder, SILKLTPAnalysisTraceSnapshot)

// WithSILKLTPAnalysisTraceHook installs a test-only callback for encoder LTP
// residual operands and output during fn.
func WithSILKLTPAnalysisTraceHook(cb func(*Encoder, SILKLTPAnalysisTraceSnapshot), fn func()) {
	previous := silkLTPAnalysisTraceHook
	silkLTPAnalysisTraceHook = cb
	defer func() {
		silkLTPAnalysisTraceHook = previous
	}()
	fn()
}

func ltpAnalysisTraceActive() bool { return silkLTPAnalysisTraceHook != nil }

func recordSILKLTPAnalysisTrace(e *Encoder, snapshot SILKLTPAnalysisTraceSnapshot) {
	if silkLTPAnalysisTraceHook != nil {
		silkLTPAnalysisTraceHook(e, snapshot)
	}
}

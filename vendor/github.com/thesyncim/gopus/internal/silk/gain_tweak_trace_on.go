//go:build gopus_silk_trace

package silk

const silkGainTweakTraceEnabled = true

var silkGainTweakTraceHook func(*Encoder, SILKGainTweakSnapshot)

func recordSILKGainTweakTrace(e *Encoder, snapshot SILKGainTweakSnapshot) {
	if silkGainTweakTraceHook != nil {
		silkGainTweakTraceHook(e, snapshot)
	}
}

// WithSILKGainTweakTraceHook installs a test-only callback around the actual
// per-subframe gain adjustment for the duration of fn.
func WithSILKGainTweakTraceHook(cb func(*Encoder, SILKGainTweakSnapshot), fn func()) {
	prev := silkGainTweakTraceHook
	silkGainTweakTraceHook = cb
	defer func() {
		silkGainTweakTraceHook = prev
	}()
	fn()
}

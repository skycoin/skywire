//go:build gopus_silk_trace

package silk

const silkNLSFInterpolationTraceEnabled = true

var silkNLSFInterpolationTraceHook func(*Encoder, SILKNLSFInterpolationSnapshot)

func silkNLSFInterpolationTraceActive() bool {
	return silkNLSFInterpolationTraceHook != nil
}

// WithSILKNLSFInterpolationTraceHook installs a test-only callback for valid
// full-frame Burg analyses in the active float encoder during fn. Calls that
// skip interpolation still report their actual Burg input and result with
// selected index 4 and no interpolation candidates. Candidate segment energies
// use C double from silk_energy_FLP (silk/float/energy_FLP.c); their sum uses
// silk_float as in silk/float/find_LPC_FLP.c.
func WithSILKNLSFInterpolationTraceHook(cb func(*Encoder, SILKNLSFInterpolationSnapshot), fn func()) {
	previous := silkNLSFInterpolationTraceHook
	silkNLSFInterpolationTraceHook = cb
	defer func() {
		silkNLSFInterpolationTraceHook = previous
	}()
	fn()
}

func recordSILKNLSFInterpolationTrace(e *Encoder, snapshot SILKNLSFInterpolationSnapshot) {
	if silkNLSFInterpolationTraceHook != nil {
		silkNLSFInterpolationTraceHook(e, snapshot)
	}
}

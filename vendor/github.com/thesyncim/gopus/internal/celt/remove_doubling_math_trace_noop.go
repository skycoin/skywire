//go:build gopus_celt_trace && !gopus_remove_doubling_trace && !gopus_fixed_point

package celt

const removeDoublingMathTraceCaptureEnabled = false

func beginRemoveDoublingMathTrace([]float32, int, int, int) bool { return false }

func finishRemoveDoublingMathTrace() (EncodeRemoveDoublingMathTrace, bool) {
	return EncodeRemoveDoublingMathTrace{}, false
}

func recordRemoveDoublingDual(float32, float32) {}

func recordRemoveDoublingYY(int, float32, float32, float32, float32) {}

func recordRemoveDoublingGain(float32, float32, float32, float32, float32, float32) {}

//go:build gopus_fixed_point && !gopus_qext

package fixedpoint

// combFilterPFFixedWindow uses libopus's Q15 celt_coef window in the
// FIXED_POINT build without ENABLE_QEXT.
func (e *CELTEncoder) combFilterPFFixedWindow(y []int32, yOff int, x []int32, xOff, t0, t1, n int,
	g0, g1 int16, tapset0, tapset1 int, overlap int,
) {
	combFilterPF(y, yOff, x, xOff, t0, t1, n, g0, g1, tapset0, tapset1, e.window, overlap)
}

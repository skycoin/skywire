//go:build gopus_fixed_point && gopus_qext

package fixedpoint

// combFilterPFFixedWindow follows the coefficient width selected by the
// ENABLE_QEXT build. CELT's 48 kHz mode stores its overlap window as Q31 even
// when the runtime QEXT side coder is disabled.
func (e *CELTEncoder) combFilterPFFixedWindow(y []int32, yOff int, x []int32, xOff, t0, t1, n int,
	g0, g1 int16, tapset0, tapset1 int, overlap int,
) {
	window := e.qext.customWindow
	if window == nil {
		window = staticQEXTMDCT48000Window[:]
		if overlap > len(window) {
			window = staticQEXTMDCT96000Window[:]
		}
	}
	if overlap > len(window) {
		panic("fixed-QEXT prefilter overlap exceeds the selected Q31 mode window")
	}
	window = window[:overlap]
	CombFilterQEXTPF(y, yOff, x, xOff, t0, t1, n, g0, g1,
		tapset0, tapset1, window, overlap)
}

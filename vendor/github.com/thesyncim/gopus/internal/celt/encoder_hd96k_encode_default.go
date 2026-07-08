//go:build !gopus_qext

package celt

// analysisOverlap returns the MDCT-analysis overlap. Without the gopus_qext
// build tag the native 96 kHz HD mode does not exist, so this is always the
// 48 kHz package constant and the 48 kHz path is unchanged.
func (e *Encoder) analysisOverlap() int {
	if e.hd96kOverlap > 0 {
		return e.hd96kOverlap
	}
	return Overlap
}

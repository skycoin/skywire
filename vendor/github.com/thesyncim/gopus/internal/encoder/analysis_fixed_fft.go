//go:build gopus_fixed_point && !gopus_qext

package encoder

import "github.com/thesyncim/gopus/internal/fixedpoint"

var analysisFixedFFTState = fixedpoint.StaticCELT48000FFTState()

func (s *TonalityAnalysisState) analysisRunFixedFFT() {
	fixedpoint.OpusFFT(analysisFixedFFTState, s.fixed.fftIn[:], s.fixed.fftOut[:])
}

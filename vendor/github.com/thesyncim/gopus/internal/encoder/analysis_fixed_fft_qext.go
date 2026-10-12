//go:build gopus_fixed_point && gopus_qext

package encoder

import "github.com/thesyncim/gopus/internal/fixedpoint"

var analysisFixedQEXTFFTState = fixedpoint.StaticQEXTCELT48000FFTState()

func (s *TonalityAnalysisState) analysisRunFixedFFT() {
	analysisFixedQEXTFFTState.OpusFFT(s.fixed.fftIn[:], s.fixed.fftOut[:])
}

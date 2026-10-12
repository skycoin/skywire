//go:build !gopus_fixed_point

package encoder

import "github.com/thesyncim/gopus/internal/celt"

type fixedAnalysisState struct{}

func (s *TonalityAnalysisState) analysisDownmixAndResample(pcm []float32, channels, dstStart, subframe, offset int) float32 {
	return s.downmixAndResample(pcm, channels, s.InMem[dstStart:], subframe, offset)
}

func (s *TonalityAnalysisState) analysisIsDigitalSilence() bool {
	return analysisIsDigitalSilence32(s.InMem[:AnalysisBufSize], int(s.LSBDepth))
}

func (s *TonalityAnalysisState) analysisPrepareFFTInput() {
	inBuf := s.scratchFFTIn[:]
	for i := range 240 {
		w := analysisWindow[i]
		inBuf[i] = complex(w*s.InMem[i], w*s.InMem[240+i])
		inBuf[480-i-1] = complex(w*s.InMem[480-i-1], w*s.InMem[480+240-i-1])
	}
}

func (s *TonalityAnalysisState) analysisShiftInput() {
	copy(s.InMem[:240], s.InMem[AnalysisBufSize-240:AnalysisBufSize])
}

func (s *TonalityAnalysisState) analysisRunFFT() {
	if cap(s.scratchFFTKiss) < 480 {
		s.scratchFFTKiss = make([]celt.KissCpx, 480)
	}
	fft480(&s.scratchFFTOut, &s.scratchFFTIn, s.scratchFFTKiss[:480])
}

func (s *TonalityAnalysisState) analysisEnergyScale() float32 {
	return (1.0 / (celtSigScale * celtSigScale)) * analysisFFTEnergyScale
}

func (s *TonalityAnalysisState) analysisHighBandEnergy(hpEner float32) float32 {
	return round32(hpEner * (1.0 / (60.0 * 60.0)))
}

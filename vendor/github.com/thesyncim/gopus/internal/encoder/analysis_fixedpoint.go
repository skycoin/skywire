//go:build gopus_fixed_point

package encoder

import "github.com/thesyncim/gopus/internal/fixedpoint"

const (
	analysisFixedInputCap = 2880                       // max transformed source length for a 60 ms frame
	analysisFixedEvenCoef = int16(19904)               // QCONST16(0.6074371f, 15)
	analysisFixedOddCoef  = int16(4936)                // QCONST16(0.15063f, 15), rounded as fixed_generic.h.
	analysisFixedCompens  = float32(1.0 / 134217728.0) // SCALE_COMPENS, SIG_SHIFT=12.
)

// fixedAnalysisState holds the fixed-point input and FFT used by
// src/analysis.c when FIXED_POINT is enabled. Feature, tonality and classifier
// state remains float32 because libopus casts the integer FFT output to float
// before computing those values.
type fixedAnalysisState struct {
	inMem        [AnalysisBufSize]int32
	downmixState [3]int32
	downmixTmp   [analysisFixedInputCap]int32
	repeatTmp    [analysisFixedInputCap]int32
	fftIn        [480]fixedpoint.FFTCpx
	fftOut       [480]fixedpoint.FFTCpx
}

func (s *TonalityAnalysisState) analysisDownmixAndResample(pcm []float32, channels, dstStart, subframe, offset int) float32 {
	if subframe <= 0 || channels <= 0 || dstStart < 0 || dstStart > len(s.fixed.inMem) {
		return 0
	}
	if subframe > analysisFixedInputCap || offset < 0 {
		return 0
	}
	switch s.Fs {
	case 48000:
		subframe *= 2
		offset *= 2
	case 16000:
		subframe = subframe * 2 / 3
		offset = offset * 2 / 3
	case 24000:
	default:
		return 0
	}
	if subframe > len(s.fixed.downmixTmp) || offset+subframe > len(pcm)/channels {
		return 0
	}
	tmp := s.fixed.downmixTmp[:subframe]
	for j := range tmp {
		base := (j + offset) * channels
		v := fixedAnalysisFloatToSig(pcm[base])
		for ch := 1; ch < channels; ch++ {
			v = int32(uint32(v) + uint32(fixedAnalysisFloatToSig(pcm[base+ch])))
		}
		// downmix_and_resample halves the all-channel stereo sum after each
		// channel has passed through FLOAT2SIG.
		if channels == 2 {
			v >>= 1
		}
		tmp[j] = v
	}

	var hpEnergy int32
	var out []int32
	switch s.Fs {
	case 48000:
		outCount := subframe / 2
		if dstStart+outCount > len(s.fixed.inMem) {
			return 0
		}
		out = s.fixed.inMem[dstStart : dstStart+outCount]
		hpEnergy = fixedAnalysisDown2HP(s.fixed.downmixState[:], out, tmp)
	case 24000:
		if dstStart+subframe > len(s.fixed.inMem) {
			return 0
		}
		out = s.fixed.inMem[dstStart : dstStart+subframe]
		copy(out, tmp)
	case 16000:
		repeated := s.fixed.repeatTmp[:3*subframe]
		for i, v := range tmp {
			repeated[3*i], repeated[3*i+1], repeated[3*i+2] = v, v, v
		}
		outCount := len(repeated) / 2
		if dstStart+outCount > len(s.fixed.inMem) {
			return 0
		}
		out = s.fixed.inMem[dstStart : dstStart+outCount]
		fixedAnalysisDown2HP(s.fixed.downmixState[:], out, repeated)
	}
	return float32(hpEnergy)
}

func fixedAnalysisFloatToSig(sample float32) int32 {
	if sample != sample {
		return 0
	}
	v := sample * float32(1<<27) // FLOAT2SIG: 32768 << SIG_SHIFT, SIG_SHIFT=12.
	const rail = float32(65536 << 12)
	if v < -rail {
		v = -rail
	} else if v > rail {
		v = rail
	}
	return analysisFloat2Int(v)
}

func fixedAnalysisDown2HP(state []int32, out, in []int32) int32 {
	length := min(len(out), len(in)/2)
	s0, s1, s2 := state[0], state[1], state[2]
	var hpEnergy int64
	for k := range length {
		in0 := in[2*k]
		y := in0 - s0
		x := int32((int64(analysisFixedEvenCoef) * int64(y)) >> 15)
		out32 := s0 + x
		s0 = in0 + x
		out32HP := out32

		in1 := in[2*k+1]
		y = in1 - s1
		x = int32((int64(analysisFixedOddCoef) * int64(y)) >> 15)
		out32 += s1
		out32 += x
		s1 = in1 + x

		negIn1 := int32(0 - uint32(in1))
		y = negIn1 - s2
		x = int32((int64(analysisFixedOddCoef) * int64(y)) >> 15)
		out32HP += s2
		out32HP += x
		s2 = negIn1 + x

		hpEnergy += (int64(out32HP) * int64(out32HP)) >> 8
		out[k] = out32 >> 1 // HALF32 truncates by arithmetic shift.
	}
	state[0], state[1], state[2] = s0, s1, s2
	hpEnergy >>= 24 // fixed analysis uses 2*SIG_SHIFT after the per-sample /256.
	if hpEnergy > 1<<31-1 {
		hpEnergy = 1<<31 - 1
	}
	if hpEnergy < -1<<31 {
		hpEnergy = -1 << 31
	}
	return int32(hpEnergy)
}

func (s *TonalityAnalysisState) analysisIsDigitalSilence() bool {
	for _, v := range s.fixed.inMem {
		if v != 0 {
			return false
		}
	}
	return true
}

func (s *TonalityAnalysisState) analysisPrepareFFTInput() {
	for i := range 240 {
		w := analysisWindow[i]
		s.fixed.fftIn[i] = fixedpoint.FFTCpx{
			R: int32(w * float32(s.fixed.inMem[i])),
			I: int32(w * float32(s.fixed.inMem[240+i])),
		}
		reverse := 480 - i - 1
		s.fixed.fftIn[reverse] = fixedpoint.FFTCpx{
			R: int32(w * float32(s.fixed.inMem[reverse])),
			I: int32(w * float32(s.fixed.inMem[480+240-i-1])),
		}
	}
}

func (s *TonalityAnalysisState) analysisShiftInput() {
	copy(s.fixed.inMem[:240], s.fixed.inMem[AnalysisBufSize-240:AnalysisBufSize])
}

func (s *TonalityAnalysisState) analysisRunFFT() {
	s.analysisRunFixedFFT()
	for i, v := range s.fixed.fftOut {
		s.scratchFFTOut[i] = complex(float32(v.R), float32(v.I))
	}
}

func (s *TonalityAnalysisState) analysisEnergyScale() float32 {
	return analysisFixedCompens * analysisFixedCompens
}

func (s *TonalityAnalysisState) analysisHighBandEnergy(hpEner float32) float32 {
	e := round32(hpEner * (1.0 / (60.0 * 60.0)))
	oneOverQ15 := float32(1.0) / float32(fixedpoint.Q15One)
	scale := round32(round32(float32(256.0)*oneOverQ15) * oneOverQ15)
	return round32(e * scale)
}

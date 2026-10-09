//go:build gopus_fixed_point

package multistream

import (
	"math"

	"github.com/thesyncim/gopus/internal/fixedpoint"
)

const (
	surroundDBShift = 24
	surroundQ24One  = int32(1 << surroundDBShift)
	surroundOverlap = 120
	surroundMaxLM   = 3
)

// diff_table[17] in opus_multistream_encoder.c has nine explicit GCONST
// entries; C zero-initializes the remaining eight entries. The full length is
// required because diff values in [4, 8) index entries 8 through 16.
var surroundLogSumDiffQ24 = [17]int32{
	8388608, 4907022, 2700528, 1425434, 733691, 372406, 187635, 94181, 47183,
}

type surroundAnalysisState struct {
	bandSMRQ24   []int32
	windowMem    []int32
	preemphMem   []int32
	inputScratch []int32
	bandScratch  [surroundBands]int32
	logScratch   [surroundBands]int32
	streamMask   [2 * surroundBands]int32
	bandAnalyzer *fixedpoint.SurroundAnalysis
}

func newSurroundAnalysisState(channels, streams int) surroundAnalysisState {
	return surroundAnalysisState{
		bandSMRQ24:   make([]int32, channels*surroundBands),
		windowMem:    make([]int32, channels*surroundOverlap),
		preemphMem:   make([]int32, channels),
		bandAnalyzer: fixedpoint.NewSurroundAnalysis(),
	}
}

func (s *surroundAnalysisState) reset() {
	clear(s.bandSMRQ24)
	clear(s.windowMem)
	clear(s.preemphMem)
	clear(s.inputScratch)
	clear(s.streamMask[:])
}

func (s *surroundAnalysisState) applyEnergyMasks(e *Encoder, frameSize int, input encodeInput) {
	if !e.isSurroundMapping() || e.restrictedSilk {
		return
	}
	if !s.computeBandSMRQ24(e, frameSize, input) {
		return
	}
	for stream := 0; stream < e.streams; stream++ {
		enc := e.encoders[stream]
		if stream == e.lfeStream {
			enc.SetCELTEnergyMaskQ24(nil)
			continue
		}
		c1, c2 := streamSourceChannels(e.mapping, e.coupledStreams, stream)
		copy(s.streamMask[:surroundBands], s.bandSMRQ24[c1*surroundBands:(c1+1)*surroundBands])
		mask := s.streamMask[:surroundBands]
		if c2 >= 0 {
			copy(s.streamMask[surroundBands:], s.bandSMRQ24[c2*surroundBands:(c2+1)*surroundBands])
			mask = s.streamMask[:2*surroundBands]
		}
		enc.SetCELTEnergyMaskQ24(mask)
	}
}

func (s *surroundAnalysisState) ensureInputScratch(size int) []int32 {
	if cap(s.inputScratch) < size {
		s.inputScratch = make([]int32, size)
	}
	return s.inputScratch[:size]
}

func (s *surroundAnalysisState) computeBandSMRQ24(e *Encoder, frameSize int, input encodeInput) bool {
	if frameSize <= 0 || e.inputChannels < 3 || e.inputChannels > 8 {
		return false
	}
	need := frameSize * e.inputChannels
	if len(input.f32) < need || (len(input.i16) != 0 && len(input.i16) < need) ||
		len(s.bandSMRQ24) < e.inputChannels*surroundBands {
		return false
	}
	var pos [8]int
	if !channelPositions(e.inputChannels, pos[:]) {
		return false
	}
	upsample := resamplingFactor(int(e.sampleRate))
	if upsample <= 0 {
		return false
	}
	analysisFrameSize := frameSize * upsample
	freqSize, ok := surroundAnalysisFreqSize(analysisFrameSize)
	if !ok || analysisFrameSize%freqSize != 0 {
		return false
	}
	lm := 0
	for lm < surroundMaxLM && (120<<lm) != freqSize {
		lm++
	}
	if (120 << lm) != freqSize {
		return false
	}
	nbFrames := analysisFrameSize / freqSize
	in := s.ensureInputScratch(surroundOverlap + analysisFrameSize)
	var maskLogE [3][surroundBands]int32
	for c := range 3 {
		for i := range surroundBands {
			maskLogE[c][i] = -28 * surroundQ24One
		}
	}
	for ch := 0; ch < e.inputChannels; ch++ {
		copy(in[:surroundOverlap], s.windowMem[ch*surroundOverlap:(ch+1)*surroundOverlap])
		clear(in[surroundOverlap:])
		for i := 0; i < frameSize; i++ {
			index := i*e.inputChannels + ch
			var x int32
			if len(input.i16) != 0 {
				x = int32(input.i16[index]) << 12 // INT16TOSIG in FIXED_POINT.
			} else {
				x = surroundFloatToSig(input.f32[index])
			}
			in[surroundOverlap+i*upsample] = x
		}

		m := s.preemphMem[ch]
		for i := 0; i < analysisFrameSize; i++ {
			x := in[surroundOverlap+i]
			in[surroundOverlap+i] = x - m
			m = int32((int64(27853) * int64(x)) >> 15) // MULT16_32_Q15(mode->preemph[0], x).
		}
		s.preemphMem[ch] = m

		clear(s.bandScratch[:])
		for frame := 0; frame < nbFrames; frame++ {
			start := frame * freqSize
			end := start + freqSize + surroundOverlap
			if !s.bandAnalyzer.BandEnergyInto(in[start:end], lm, upsample, s.logScratch[:]) {
				return false
			}
			for i := range surroundBands {
				if s.logScratch[i] > s.bandScratch[i] {
					s.bandScratch[i] = s.logScratch[i]
				}
			}
		}
		fixedpoint.Amp2Log2(s.bandScratch[:], s.logScratch[:], surroundBands, surroundBands, surroundBands, 1)

		for i := 1; i < surroundBands; i++ {
			if v := s.logScratch[i-1] - surroundQ24One; v > s.logScratch[i] {
				s.logScratch[i] = v
			}
		}
		for i := surroundBands - 2; i >= 0; i-- {
			if v := s.logScratch[i+1] - 2*surroundQ24One; v > s.logScratch[i] {
				s.logScratch[i] = v
			}
		}
		copy(s.bandSMRQ24[ch*surroundBands:(ch+1)*surroundBands], s.logScratch[:])

		switch pos[ch] {
		case 1:
			for i := range surroundBands {
				maskLogE[0][i] = surroundLogSumQ24(maskLogE[0][i], s.logScratch[i])
			}
		case 3:
			for i := range surroundBands {
				maskLogE[2][i] = surroundLogSumQ24(maskLogE[2][i], s.logScratch[i])
			}
		case 2:
			for i := range surroundBands {
				smr := s.logScratch[i] - surroundQ24One/2
				maskLogE[0][i] = surroundLogSumQ24(maskLogE[0][i], smr)
				maskLogE[2][i] = surroundLogSumQ24(maskLogE[2][i], smr)
			}
		}
		copy(s.windowMem[ch*surroundOverlap:(ch+1)*surroundOverlap], in[analysisFrameSize:analysisFrameSize+surroundOverlap])
	}

	for i := range surroundBands {
		maskLogE[1][i] = min(maskLogE[0][i], maskLogE[2][i])
	}
	channelOffsetInput := int32(2<<14) / int32(e.inputChannels-1)
	channelOffset := int32(fixedpoint.CeltLog2(channelOffsetInput)) >> 1
	for c := range 3 {
		for i := range surroundBands {
			maskLogE[c][i] += channelOffset
		}
	}
	for ch := 0; ch < e.inputChannels; ch++ {
		row := s.bandSMRQ24[ch*surroundBands : (ch+1)*surroundBands]
		if pos[ch] == 0 {
			clear(row)
			continue
		}
		mask := maskLogE[pos[ch]-1]
		for i := range surroundBands {
			row[i] -= mask[i]
		}
	}
	return true
}

func surroundLogSumQ24(a, b int32) int32 {
	max, diff := a, a-b
	if b > a {
		max, diff = b, b-a
	}
	if diff >= 8*surroundQ24One {
		return int32(int16(max))
	}
	low := int(diff >> (surroundDBShift - 1))
	frac := int16((diff - int32(low<<(surroundDBShift-1))) >> (surroundDBShift - 16))
	delta := surroundLogSumDiffQ24[low+1] - surroundLogSumDiffQ24[low]
	// logSum is declared opus_val16 in libopus, even in FIXED_POINT builds.
	// Its celt_glog result therefore narrows to signed 16 bits before callers
	// store it back into their 32-bit maskLogE entries.
	return int32(int16(max + surroundLogSumDiffQ24[low] + int32((int64(frac)*int64(delta))>>15)))
}

func surroundFloatToSig(sample float32) int32 {
	x := sample * float32(1<<23) // FLOAT2INT24 followed by RES2SIG shift.
	const limit = float32(1 << 24)
	if !(x > -limit) {
		x = -limit
	} else if x > limit {
		x = limit
	}
	return roundFloat32ToInt32Even(x) << 4
}

func roundFloat32ToInt32Even(value float32) int32 {
	bits := math.Float32bits(value)
	negative := bits>>31 != 0
	exponent := int((bits>>23)&0xff) - 127
	mantissa := bits&0x7fffff | 1<<23
	if exponent < -1 {
		return 0
	}
	var integer uint32
	if exponent >= 23 {
		integer = mantissa << uint(exponent-23)
	} else {
		shift := uint(23 - exponent)
		integer = mantissa >> shift
		remainder := mantissa & ((uint32(1) << shift) - 1)
		half := uint32(1) << (shift - 1)
		if remainder > half || (remainder == half && integer&1 != 0) {
			integer++
		}
	}
	if negative {
		return -int32(integer)
	}
	return int32(integer)
}

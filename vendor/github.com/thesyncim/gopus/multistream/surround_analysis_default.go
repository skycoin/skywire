//go:build !gopus_fixed_point

package multistream

import (
	"math"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/opusmath"
)

type surroundAnalysisState struct {
	bandSMR          []float32
	streamEnergyMask []float32
	windowMem        []float32
	preemphMem       []float32
	inputScratch     []float32
	bandScratch      [surroundBands]float32
	mdctScratch      celt.MDCTForwardScratch
	mdctCoeffs       []float32
	bandEncoder      *celt.Encoder
}

func newSurroundAnalysisState(channels, streams int) surroundAnalysisState {
	return surroundAnalysisState{
		bandSMR:          make([]float32, channels*surroundBands),
		streamEnergyMask: make([]float32, streams*2*surroundBands),
		windowMem:        make([]float32, channels*celt.Overlap),
		preemphMem:       make([]float32, channels),
		bandEncoder:      celt.NewEncoder(1),
	}
}

func (s *surroundAnalysisState) reset() {
	clear(s.bandSMR)
	clear(s.streamEnergyMask)
	clear(s.windowMem)
	clear(s.preemphMem)
}

func (s *surroundAnalysisState) applyEnergyMasks(e *Encoder, frameSize int, input encodeInput) {
	if !e.isSurroundMapping() || e.restrictedSilk {
		return
	}
	needed := e.inputChannels * surroundBands
	if cap(s.bandSMR) < needed {
		s.bandSMR = make([]float32, needed)
	}
	s.bandSMR = s.bandSMR[:needed]
	if !s.computeBandSMR(e, input.f32, frameSize, s.bandSMR) {
		return
	}

	maskSize := e.streams * 2 * surroundBands
	if cap(s.streamEnergyMask) < maskSize {
		s.streamEnergyMask = make([]float32, maskSize)
	}
	s.streamEnergyMask = s.streamEnergyMask[:maskSize]
	for stream := 0; stream < e.streams; stream++ {
		enc := e.encoders[stream]
		if stream == e.lfeStream {
			enc.SetCELTEnergyMask(nil)
			continue
		}
		c1, c2 := streamSourceChannels(e.mapping, e.coupledStreams, stream)
		mask := s.streamEnergyMask[stream*2*surroundBands : stream*2*surroundBands+surroundBands]
		copy(mask, s.bandSMR[c1*surroundBands:(c1+1)*surroundBands])
		if c2 >= 0 {
			mask = s.streamEnergyMask[stream*2*surroundBands : (stream+1)*2*surroundBands]
			copy(mask[surroundBands:], s.bandSMR[c2*surroundBands:(c2+1)*surroundBands])
		}
		enc.SetCELTEnergyMask(mask)
	}
}

func (s *surroundAnalysisState) ensureInputScratch(size int) []float32 {
	if cap(s.inputScratch) < size {
		s.inputScratch = make([]float32, size)
	}
	return s.inputScratch[:size]
}

func (s *surroundAnalysisState) computeBandSMR(e *Encoder, pcm []float32, frameSize int, bandSMR []float32) bool {
	if frameSize <= 0 || e.inputChannels < 3 || e.inputChannels > 8 ||
		len(pcm) < frameSize*e.inputChannels || len(bandSMR) < e.inputChannels*surroundBands {
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
	nbFrames := analysisFrameSize / freqSize
	overlap := celt.Overlap
	in := s.ensureInputScratch(overlap + analysisFrameSize)

	var maskLogE [3][surroundBands]float32
	for c := range 3 {
		for i := range surroundBands {
			maskLogE[c][i] = -28
		}
	}
	for ch := 0; ch < e.inputChannels; ch++ {
		copy(in[:overlap], s.windowMem[ch*overlap:(ch+1)*overlap])
		clear(in[overlap:])
		for i := range frameSize {
			in[overlap+i*upsample] = pcm[i*e.inputChannels+ch] * float32(celt.CELTSigScale)
		}

		m := s.preemphMem[ch]
		for i := range analysisFrameSize {
			x := in[overlap+i]
			in[overlap+i] = x - m
			m = celt.PreemphCoef * x
		}
		s.preemphMem[ch] = m
		var sum float32
		for _, v := range in {
			sum += v * v
		}
		if !(sum < 1e18) {
			clear(in)
			s.preemphMem[ch] = 0
		}

		for i := range surroundBands {
			s.bandScratch[i] = float32(math.Inf(-1))
		}
		if cap(s.mdctCoeffs) < freqSize {
			s.mdctCoeffs = make([]float32, freqSize)
		}
		coeffs := s.mdctCoeffs[:freqSize]
		for frame := range nbFrames {
			start := frame * freqSize
			end := start + freqSize + overlap
			s.mdctScratch.ForwardWithOverlapFloat32Into(in[start:end], overlap, coeffs)
			if upsample != 1 {
				bound := freqSize / upsample
				for i := range bound {
					coeffs[i] *= float32(upsample)
				}
				for i := bound; i < len(coeffs); i++ {
					coeffs[i] = 0
				}
			}
			var tmp [surroundBands]float32
			s.bandEncoder.ComputeBandEnergiesFloat32Into(coeffs, surroundBands, freqSize, tmp[:])
			for i := range surroundBands {
				if tmp[i] > s.bandScratch[i] {
					s.bandScratch[i] = tmp[i]
				}
			}
		}

		for i := 1; i < surroundBands; i++ {
			if s.bandScratch[i-1]-1 > s.bandScratch[i] {
				s.bandScratch[i] = s.bandScratch[i-1] - 1
			}
		}
		for i := surroundBands - 2; i >= 0; i-- {
			if s.bandScratch[i+1]-2 > s.bandScratch[i] {
				s.bandScratch[i] = s.bandScratch[i+1] - 2
			}
		}
		copy(bandSMR[ch*surroundBands:(ch+1)*surroundBands], s.bandScratch[:])

		switch pos[ch] {
		case 1:
			for i := range surroundBands {
				maskLogE[0][i] = logSum32(maskLogE[0][i], s.bandScratch[i])
			}
		case 3:
			for i := range surroundBands {
				maskLogE[2][i] = logSum32(maskLogE[2][i], s.bandScratch[i])
			}
		case 2:
			for i := range surroundBands {
				maskLogE[0][i] = logSum32(maskLogE[0][i], s.bandScratch[i]-0.5)
				maskLogE[2][i] = logSum32(maskLogE[2][i], s.bandScratch[i]-0.5)
			}
		}
		copy(s.windowMem[ch*overlap:(ch+1)*overlap], in[analysisFrameSize:analysisFrameSize+overlap])
	}

	for i := range surroundBands {
		maskLogE[1][i] = min(maskLogE[0][i], maskLogE[2][i])
	}
	channelOffset := 0.5 * opusmath.CeltLog2(2.0/float32(e.inputChannels-1))
	for c := range 3 {
		for i := range surroundBands {
			maskLogE[c][i] += channelOffset
		}
	}
	for ch := 0; ch < e.inputChannels; ch++ {
		row := bandSMR[ch*surroundBands : (ch+1)*surroundBands]
		if pos[ch] == 0 {
			clear(row)
			continue
		}
		mask := maskLogE[pos[ch]-1][:]
		for i := range surroundBands {
			row[i] -= mask[i]
		}
	}
	return true
}

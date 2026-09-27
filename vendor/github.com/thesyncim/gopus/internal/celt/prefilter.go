package celt

import (
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/util"
)

type prefilterResult struct {
	on     bool
	pitch  int
	qg     int
	tapset int
	gain   float32
}

// runPrefilter applies the CELT prefilter (comb filter) and returns the
// postfilter parameters to signal in the bitstream.
// This mirrors libopus run_prefilter() in celt_encoder.c.
func (e *Encoder) runPrefilter(preemph []float32, frameSize int, tapset int, enabled bool, tfEstimate float32, nbAvailableBytes int, toneFreq, toneishness, maxPitchRatio float32) prefilterResult {
	result := prefilterResult{on: false, pitch: combFilterMinPeriod, qg: 0, tapset: tapset, gain: 0}
	channels := int(e.channels)
	if channels <= 0 || frameSize <= 0 || len(preemph) == 0 {
		return result
	}

	if tapset < 0 {
		tapset = 0
	}
	if tapset >= len(combFilterGains) {
		tapset = len(combFilterGains) - 1
	}

	qextScale := e.combScale()
	maxPeriod := e.combMaxPeriod()
	minPeriod := e.combMinPeriod()
	// e.prefilterPeriod is stored at the unscaled COMBFILTER range (the comb
	// filter runs at that scale; only the analysis buffers/search use the
	// QEXT-scaled period). Clamp it the same way libopus clamps
	// st->prefilter_period inside run_prefilter.
	prevPeriod := min(max(e.prefilterPeriod, combFilterMinPeriod), combFilterMaxPeriod-2)
	prevTapset := max(e.prefilterTapset, 0)
	if prevTapset >= len(combFilterGains) {
		prevTapset = len(combFilterGains) - 1
	}
	if !enabled && e.prefilterGain == 0 {
		overlap := min(e.analysisOverlap(), frameSize)
		e.updatePrefilterNoopStateFromPreemph(preemph, frameSize, channels, overlap)
		e.prefilterPeriod = combFilterMinPeriod
		e.prefilterGain = 0
		e.prefilterTapset = tapset
		result.tapset = tapset
		return result
	}
	perChanLen := maxPeriod + frameSize
	pre := ensureSigSliceNoClear(&e.scratch.prefilterPre, perChanLen*channels)

	if channels == 1 {
		hist := e.prefilterMem[:maxPeriod]
		preCh := pre[:perChanLen]
		copy(preCh[:maxPeriod], hist)
		// celtSig is a float32 alias, so the per-sample copy is a plain memmove.
		copy(preCh[maxPeriod:maxPeriod+frameSize], preemph[:frameSize])
	} else {
		histL := e.prefilterMem[:maxPeriod]
		histR := e.prefilterMem[maxPeriod : 2*maxPeriod]
		preL := pre[:perChanLen]
		preR := pre[perChanLen : 2*perChanLen]
		copy(preL[:maxPeriod], histL)
		copy(preR[:maxPeriod], histR)
		for i := range frameSize {
			preL[maxPeriod+i] = celtSig(preemph[2*i])
			preR[maxPeriod+i] = celtSig(preemph[2*i+1])
		}
	}
	pitchIndex := combFilterMinPeriod
	gain1 := float32(0)
	qg := 0
	pfOn := false

	if enabled && toneishness > 0.99 {
		// Aliased postfilter above 24 kHz: compare/scale the detected tone
		// frequency through QEXT_SCALE (2 at native 96 kHz). The resulting pitch
		// index stays at the unscaled COMBFILTER_MAXPERIOD range (libopus does
		// not /= qext_scale on this branch).
		freq := toneFreq
		const pi32 = float32(3.14159265358979323846)
		if freq*float32(qextScale) >= pi32 {
			freq = pi32 - freq
		}
		multiple := 1
		for freq*float32(qextScale) >= float32(multiple)*0.39 {
			multiple++
		}
		if freq*float32(qextScale) > 0.006148 {
			pitchIndex = min(int(0.5+2*pi32*float32(multiple)/(freq*float32(qextScale))), combFilterMaxPeriod-2)
		} else {
			pitchIndex = combFilterMinPeriod
		}
		gain1 = 0.75
	} else if enabled && e.complexity >= 5 {
		pitchBufLen := max((maxPeriod+frameSize)>>1, 1)
		pitchBuf := ensureFloat32Slice(&e.scratch.prefilterPitchBuf, pitchBufLen)
		pitchDownsampleSig(pre, pitchBuf, pitchBufLen, channels, 2)
		maxPitch := max(maxPeriod-3*minPeriod, 1)
		searchOut := pitchSearch(pitchBuf[maxPeriod>>1:], pitchBuf, frameSize, maxPitch, &e.scratch)
		pitchIndex = searchOut
		pitchIndex = maxPeriod - pitchIndex
		gain1 = removeDoubling(pitchBuf, maxPeriod, minPeriod, frameSize, &pitchIndex, e.prefilterPeriod, e.prefilterGain, &e.scratch)
		if pitchIndex > maxPeriod-2*qextScale {
			pitchIndex = maxPeriod - 2*qextScale
		}
		// Bring the pitch index back to the unscaled COMBFILTER range used by
		// the comb filter (libopus: pitch_index /= qext_scale under ENABLE_QEXT).
		pitchIndex /= qextScale
		gain1 *= 0.7
		if e.packetLoss > 2 {
			gain1 *= 0.5
		}
		if e.packetLoss > 4 {
			gain1 *= 0.5
		}
		if e.packetLoss > 8 {
			gain1 = 0
		}
	} else {
		gain1 = 0
		pitchIndex = combFilterMinPeriod
	}
	// Match libopus run_prefilter() scaling by analysis->max_pitch_ratio.
	if maxPitchRatio < 0 {
		maxPitchRatio = 0
	}
	if maxPitchRatio > 1 {
		maxPitchRatio = 1
	}
	gain1 *= float32(maxPitchRatio)

	// Gain threshold for enabling the prefilter/postfilter
	pfThreshold := float32(0.2)
	if util.Abs(pitchIndex-e.prefilterPeriod)*10 > pitchIndex {
		pfThreshold += 0.2
		if tfEstimate > 0.98 {
			gain1 = 0
		}
	}
	if nbAvailableBytes < 25 {
		pfThreshold += 0.1
	}
	if nbAvailableBytes < 35 {
		pfThreshold += 0.1
	}
	if e.prefilterGain > 0.4 {
		pfThreshold -= 0.1
	}
	if e.prefilterGain > 0.55 {
		pfThreshold -= 0.1
	}
	if pfThreshold < 0.2 {
		pfThreshold = 0.2
	}
	if gain1 < pfThreshold {
		gain1 = 0
		pfOn = false
		qg = 0
	} else {
		if abs32(gain1-e.prefilterGain) < 0.1 {
			gain1 = e.prefilterGain
		}
		qg = min(max(int(0.5+gain1*32.0/3.0)-1, 0), 7)
		gain1 = float32(0.09375) * float32(qg+1)
		pfOn = true
	}

	overlap := min(e.analysisOverlap(), frameSize)
	if gain1 == 0 && e.prefilterGain == 0 {
		e.updatePrefilterNoopState(pre, perChanLen, frameSize, channels, overlap)
		e.prefilterPeriod = pitchIndex
		e.prefilterGain = 0
		e.prefilterTapset = tapset
		result.pitch = pitchIndex
		result.tapset = tapset
		return result
	}

	out := ensureSigSliceNoClear(&e.scratch.prefilterOut, perChanLen*channels)
	mode := e.modeConfig(frameSize)
	shortMdctSize := frameSize / mode.ShortBlocks
	offset := max(shortMdctSize-overlap, 0)
	window := GetWindowBufferF32(overlap)

	var before [2]opusVal32
	var after [2]opusVal32
	for ch := range channels {
		preCh := pre[ch*perChanLen : (ch+1)*perChanLen]
		outCh := out[ch*perChanLen : (ch+1)*perChanLen]
		preSub := preCh[maxPeriod : maxPeriod+frameSize]
		before[ch] = absSumSig(preSub)
		if offset > 0 {
			combFilterWithInputSig(outCh, preCh, maxPeriod, prevPeriod, prevPeriod, offset, -e.prefilterGain, -e.prefilterGain, prevTapset, prevTapset, nil, 0)
		}
		combFilterWithInputSig(outCh, preCh, maxPeriod+offset, prevPeriod, pitchIndex, frameSize-offset, -e.prefilterGain, -gain1, prevTapset, tapset, window, overlap)
		outSub := outCh[maxPeriod : maxPeriod+frameSize]
		after[ch] = absSumSig(outSub)
	}

	cancelPitch := false
	if channels == 2 {
		gain := opusVal32(gain1)
		thresh0 := opusVal32(0.25)*gain*before[0] + opusVal32(0.01)*before[1]
		thresh1 := opusVal32(0.25)*gain*before[1] + opusVal32(0.01)*before[0]
		if after[0]-before[0] > thresh0 || after[1]-before[1] > thresh1 {
			cancelPitch = true
		}
		if before[0]-after[0] < thresh0 && before[1]-after[1] < thresh1 {
			cancelPitch = true
		}
	} else {
		if after[0] > before[0] {
			cancelPitch = true
		}
	}

	if cancelPitch {
		for ch := range channels {
			preCh := pre[ch*perChanLen : (ch+1)*perChanLen]
			outCh := out[ch*perChanLen : (ch+1)*perChanLen]
			copy(outCh[maxPeriod:maxPeriod+frameSize], preCh[maxPeriod:maxPeriod+frameSize])
			combFilterWithInputSig(outCh, preCh, maxPeriod+offset, prevPeriod, pitchIndex, overlap, -e.prefilterGain, 0, prevTapset, tapset, window, overlap)
		}
		gain1 = 0
		pfOn = false
		qg = 0
	}

	if overlap > 0 {
		need := channels * overlap
		if len(e.overlapBuffer) < need {
			newBuf := make([]celtSig, need)
			copy(newBuf, e.overlapBuffer)
			e.overlapBuffer = newBuf
		}
	}

	if channels == 1 {
		preCh := pre[:perChanLen]
		outCh := out[:perChanLen]
		mem := e.prefilterMem[:maxPeriod]
		if frameSize > maxPeriod {
			copy(mem, preCh[frameSize:frameSize+maxPeriod])
		} else {
			copy(mem, mem[frameSize:])
			copy(mem[maxPeriod-frameSize:], preCh[maxPeriod:maxPeriod+frameSize])
		}
		outSub2 := outCh[maxPeriod : maxPeriod+frameSize]
		copySigToFloat32(preemph[:frameSize], outSub2)
		if overlap > 0 && len(e.overlapBuffer) >= overlap && frameSize >= overlap {
			hist := e.overlapBuffer[:overlap]
			copy(hist, outSub2[frameSize-overlap:])
		}
	} else {
		preL := pre[:perChanLen]
		preR := pre[perChanLen : 2*perChanLen]
		outL := out[maxPeriod : maxPeriod+frameSize]
		outR := out[perChanLen+maxPeriod : perChanLen+maxPeriod+frameSize]
		memL := e.prefilterMem[:maxPeriod]
		memR := e.prefilterMem[maxPeriod : 2*maxPeriod]
		if frameSize > maxPeriod {
			copy(memL, preL[frameSize:frameSize+maxPeriod])
			copy(memR, preR[frameSize:frameSize+maxPeriod])
		} else {
			copy(memL, memL[frameSize:])
			copy(memL[maxPeriod-frameSize:], preL[maxPeriod:maxPeriod+frameSize])
			copy(memR, memR[frameSize:])
			copy(memR[maxPeriod-frameSize:], preR[maxPeriod:maxPeriod+frameSize])
		}
		interleaveSigToFloat32(outL, outR, preemph[:frameSize*2])
		if overlap > 0 && len(e.overlapBuffer) >= channels*overlap && frameSize >= overlap {
			histL := e.overlapBuffer[:overlap]
			histR := e.overlapBuffer[overlap : 2*overlap]
			copy(histL, outL[frameSize-overlap:])
			copy(histR, outR[frameSize-overlap:])
		}
	}

	e.prefilterPeriod = pitchIndex
	e.prefilterGain = gain1
	e.prefilterTapset = tapset

	result.on = pfOn
	result.pitch = pitchIndex
	result.qg = qg
	result.tapset = tapset
	result.gain = gain1
	return result
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func (e *Encoder) updatePrefilterNoopState(pre []celtSig, perChanLen, frameSize, channels, overlap int) {
	if channels <= 0 || frameSize <= 0 || len(pre) < perChanLen*channels {
		return
	}
	maxPeriod := e.combMaxPeriod()
	if overlap > 0 {
		need := channels * overlap
		if len(e.overlapBuffer) < need {
			newBuf := make([]celtSig, need)
			copy(newBuf, e.overlapBuffer)
			e.overlapBuffer = newBuf
		}
	}

	for ch := range channels {
		preCh := pre[ch*perChanLen : (ch+1)*perChanLen]
		mem := e.prefilterMem[ch*maxPeriod : (ch+1)*maxPeriod]
		if frameSize > maxPeriod {
			copy(mem, preCh[frameSize:frameSize+maxPeriod])
		} else {
			copy(mem, mem[frameSize:])
			copy(mem[maxPeriod-frameSize:], preCh[maxPeriod:maxPeriod+frameSize])
		}
		if overlap > 0 && frameSize >= overlap && len(e.overlapBuffer) >= (ch+1)*overlap {
			hist := e.overlapBuffer[ch*overlap : (ch+1)*overlap]
			copy(hist, preCh[maxPeriod+frameSize-overlap:maxPeriod+frameSize])
		}
	}
}

func (e *Encoder) updatePrefilterNoopStateFromPreemph(preemph []float32, frameSize, channels, overlap int) {
	if channels <= 0 || frameSize <= 0 || len(preemph) < frameSize*channels {
		return
	}
	maxPeriod := e.combMaxPeriod()
	if overlap > 0 {
		need := channels * overlap
		if len(e.overlapBuffer) < need {
			newBuf := make([]celtSig, need)
			copy(newBuf, e.overlapBuffer)
			e.overlapBuffer = newBuf
		}
	}

	if channels == 1 {
		mem := e.prefilterMem[:maxPeriod]
		if frameSize > maxPeriod {
			copyFloat32ToSig(mem, preemph[frameSize-maxPeriod:frameSize])
		} else {
			copy(mem, mem[frameSize:])
			copyFloat32ToSig(mem[maxPeriod-frameSize:], preemph[:frameSize])
		}
		if overlap > 0 && frameSize >= overlap && len(e.overlapBuffer) >= overlap {
			copyFloat32ToSig(e.overlapBuffer[:overlap], preemph[frameSize-overlap:frameSize])
		}
		return
	}

	if channels == 2 {
		memL := e.prefilterMem[:maxPeriod]
		memR := e.prefilterMem[maxPeriod : 2*maxPeriod]
		if frameSize > maxPeriod {
			src := (frameSize - maxPeriod) * 2
			for i := range maxPeriod {
				memL[i] = celtSig(preemph[src])
				memR[i] = celtSig(preemph[src+1])
				src += 2
			}
		} else {
			copy(memL, memL[frameSize:])
			copy(memR, memR[frameSize:])
			dst := maxPeriod - frameSize
			src := 0
			for i := range frameSize {
				memL[dst+i] = celtSig(preemph[src])
				memR[dst+i] = celtSig(preemph[src+1])
				src += 2
			}
		}
		if overlap > 0 && frameSize >= overlap && len(e.overlapBuffer) >= 2*overlap {
			histL := e.overlapBuffer[:overlap]
			histR := e.overlapBuffer[overlap : 2*overlap]
			src := (frameSize - overlap) * 2
			for i := range overlap {
				histL[i] = celtSig(preemph[src])
				histR[i] = celtSig(preemph[src+1])
				src += 2
			}
		}
		return
	}

	for ch := range channels {
		mem := e.prefilterMem[ch*maxPeriod : (ch+1)*maxPeriod]
		if frameSize > maxPeriod {
			src := (frameSize-maxPeriod)*channels + ch
			for i := range maxPeriod {
				mem[i] = celtSig(preemph[src])
				src += channels
			}
		} else {
			copy(mem, mem[frameSize:])
			dst := maxPeriod - frameSize
			src := ch
			for i := range frameSize {
				mem[dst+i] = celtSig(preemph[src])
				src += channels
			}
		}
		if overlap > 0 && frameSize >= overlap && len(e.overlapBuffer) >= (ch+1)*overlap {
			hist := e.overlapBuffer[ch*overlap : (ch+1)*overlap]
			src := (frameSize-overlap)*channels + ch
			for i := range overlap {
				hist[i] = celtSig(preemph[src])
				src += channels
			}
		}
	}
}

func pitchDownsampleSig(x []celtSig, xLP []float32, length, channels, factor int) {
	if length <= 0 || factor <= 0 || len(xLP) < length {
		return
	}
	const (
		firQuarter = float32(0.25)
		firHalf    = float32(0.5)
	)
	handled := false
	if factor == 2 {
		if channels == 1 {
			// Sliding-window FIR: each output xLP[i] = 0.25*(x[2i-1]+x[2i+1]) + 0.5*x[2i].
			// Slicing src to exactly 2*length lets the compiler prove every window
			// access (win[0:3]) is in bounds, eliminating per-sample bounds checks.
			xLP[0] = firQuarter*float32(x[1]) + firHalf*float32(x[0])
			if length > 1 && len(x) >= 2*length {
				src := x[:2*length]
				win := src[1:] // win[0]=x[2i-1], win[1]=x[2i], win[2]=x[2i+1] at i=1
				dst := xLP[1:length]
				// 4-output unroll: consecutive outputs share y[2i+1]=y[2(i+1)-1],
				// reducing loads from 12 to 9 per 4 outputs.
				for len(dst) >= 4 && len(win) >= 9 {
					w0, w1, w2, w3, w4, w5, w6, w7, w8 := win[0], win[1], win[2], win[3], win[4], win[5], win[6], win[7], win[8]
					dst[0] = firQuarter*float32(w0) + firQuarter*float32(w2) + firHalf*float32(w1)
					dst[1] = firQuarter*float32(w2) + firQuarter*float32(w4) + firHalf*float32(w3)
					dst[2] = firQuarter*float32(w4) + firQuarter*float32(w6) + firHalf*float32(w5)
					dst[3] = firQuarter*float32(w6) + firQuarter*float32(w8) + firHalf*float32(w7)
					win = win[8:]
					dst = dst[4:]
				}
				for len(dst) > 0 && len(win) >= 3 {
					v := firQuarter*float32(win[0]) + firQuarter*float32(win[2]) + firHalf*float32(win[1])
					dst[0] = v
					win = win[2:]
					dst = dst[1:]
				}
			}
		} else if channels == 2 {
			chStride := len(x) / 2
			x0 := x[:chStride]
			x1 := x[chStride:]
			v0 := firQuarter*float32(x0[1]) + firHalf*float32(x0[0])
			v1 := firQuarter*float32(x1[1]) + firHalf*float32(x1[0])
			xLP[0] = v0 + v1
			if length > 1 && len(x0) >= 2*length && len(x1) >= 2*length {
				s0 := x0[:2*length]
				s1 := x1[:2*length]
				w0 := s0[1:]
				w1 := s1[1:]
				dst := xLP[1:length]
				// 2× unroll: w0[2] shared between pair; L and R independent (12
				// FMULs per 2 outputs saturate 4-wide dispatch with latency hiding).
				for len(dst) >= 2 && len(w0) >= 5 && len(w1) >= 5 {
					vv0_0 := firQuarter*float32(w0[0]) + firQuarter*float32(w0[2]) + firHalf*float32(w0[1])
					vv1_0 := firQuarter*float32(w1[0]) + firQuarter*float32(w1[2]) + firHalf*float32(w1[1])
					vv0_1 := firQuarter*float32(w0[2]) + firQuarter*float32(w0[4]) + firHalf*float32(w0[3])
					vv1_1 := firQuarter*float32(w1[2]) + firQuarter*float32(w1[4]) + firHalf*float32(w1[3])
					dst[0] = vv0_0 + vv1_0
					dst[1] = vv0_1 + vv1_1
					w0 = w0[4:]
					w1 = w1[4:]
					dst = dst[2:]
				}
				for len(dst) > 0 && len(w0) >= 3 && len(w1) >= 3 {
					vv0 := firQuarter*float32(w0[0]) + firQuarter*float32(w0[2]) + firHalf*float32(w0[1])
					vv1 := firQuarter*float32(w1[0]) + firQuarter*float32(w1[2]) + firHalf*float32(w1[1])
					dst[0] = vv0 + vv1
					w0 = w0[2:]
					w1 = w1[2:]
					dst = dst[1:]
				}
			}
		}
		handled = true
	}
	if !handled {
		offset := max(factor/2, 1)
		for i := 1; i < length; i++ {
			idx := factor * i
			v := firQuarter*float32(x[idx-offset]) +
				firQuarter*float32(x[idx+offset]) +
				firHalf*float32(x[idx])
			xLP[i] = v
		}
		xLP[0] = firQuarter*float32(x[offset]) + firHalf*float32(x[0])
		if channels == 2 {
			chStride := len(x) / 2
			x1 := x[chStride:]
			for i := 1; i < length; i++ {
				idx := factor * i
				v := firQuarter*float32(x1[idx-offset]) +
					firQuarter*float32(x1[idx+offset]) +
					firHalf*float32(x1[idx])
				xLP[i] += v
			}
			v := firQuarter*float32(x1[offset]) + firHalf*float32(x1[0])
			xLP[0] += v
		}
	}

	var ac [5]float32
	pitchAutocorr5F32(xLP[:length], length, &ac)

	applyCELTAutocorrNoiseAndLagWindow32(ac[:], 4)

	lpc := lpcFromAutocorr32(ac)
	tmp := float32(1.0)
	for i := range 4 {
		tmp *= float32(0.9)
		lpc[i] *= tmp
	}
	c1 := float32(0.8)
	lpc2 := [5]float32{
		lpc[0] + float32(0.8),
		lpc[1] + c1*lpc[0],
		lpc[2] + c1*lpc[1],
		lpc[3] + c1*lpc[2],
		c1 * lpc[3],
	}
	celtFIR5F32(xLP, lpc2)
}

func pitchSearch(xLP []float32, y []float32, length, maxPitch int, scratch *encoderScratch) int {
	if length <= 0 || maxPitch <= 0 {
		return 0
	}
	lag := length + maxPitch
	quarterLen := length >> 2
	quarterLag := lag >> 2
	quarterPitch := maxPitch >> 2
	halfLen := length >> 1
	halfPitch := maxPitch >> 1

	xLP4 := ensureFloat32Slice(&scratch.prefilterXLP4, quarterLen)
	yLP4 := ensureFloat32Slice(&scratch.prefilterYLP4, quarterLag)
	xcorr := ensureFloat32Slice(&scratch.prefilterXcorr, halfPitch)

	{
		_ = xLP[2*quarterLen-1]
		_ = xLP4[quarterLen-1]
		j := 0
		for ; j+3 < quarterLen; j += 4 {
			xLP4[j] = xLP[2*j]
			xLP4[j+1] = xLP[2*j+2]
			xLP4[j+2] = xLP[2*j+4]
			xLP4[j+3] = xLP[2*j+6]
		}
		for ; j < quarterLen; j++ {
			xLP4[j] = xLP[2*j]
		}
	}
	{
		_ = y[2*quarterLag-1]
		_ = yLP4[quarterLag-1]
		j := 0
		for ; j+3 < quarterLag; j += 4 {
			yLP4[j] = y[2*j]
			yLP4[j+1] = y[2*j+2]
			yLP4[j+2] = y[2*j+4]
			yLP4[j+3] = y[2*j+6]
		}
		for ; j < quarterLag; j++ {
			yLP4[j] = y[2*j]
		}
	}

	pitchXCorrFloat32Quality(xLP4, yLP4, xcorr, quarterLen, quarterPitch)
	bestPitch := [2]int{0, 0}
	findBestPitchF32(xcorr, yLP4, quarterLen, quarterPitch, &bestPitch)

	ranges := pitchSearchFineRanges(bestPitch, halfPitch)
	for _, r := range ranges {
		if r.hi < r.lo {
			continue
		}
		lo := max(r.lo-1, 0)
		hi := min(r.hi+2, halfPitch)
		clear(xcorr[lo:hi])
	}
	Syy := float32(1)
	for j := range halfLen {
		yj := float32(y[j])
		Syy += yj * yj
	}
	bestNum := [2]float32{-1, -1}
	bestDen := [2]float32{0, 0}
	fineBestPitch := [2]int{0, 1}
	i := 0
	for _, r := range ranges {
		if r.hi < r.lo {
			continue
		}
		for ; i < r.lo; i++ {
			yi := float32(y[i])
			yil := float32(y[i+halfLen])
			Syy += yil*yil - yi*yi
			if Syy < 1 {
				Syy = 1
			}
		}
		n := r.hi - r.lo + 1
		pitchXCorrFloat32Quality(xLP, y[r.lo:], xcorr[r.lo:], halfLen, n)
		for ; i <= r.hi; i++ {
			if xcorr[i] < -1 {
				xcorr[i] = -1
			}
			if xv := xcorr[i]; xv > 0 {
				xcorr16 := xv * pitchSearchXcorrScale
				num := xcorr16 * xcorr16
				if num*bestDen[1] > bestNum[1]*Syy {
					if num*bestDen[0] > bestNum[0]*Syy {
						bestNum[1] = bestNum[0]
						bestDen[1] = bestDen[0]
						fineBestPitch[1] = fineBestPitch[0]
						bestNum[0] = num
						bestDen[0] = Syy
						fineBestPitch[0] = i
					} else {
						bestNum[1] = num
						bestDen[1] = Syy
						fineBestPitch[1] = i
					}
				}
			}
			yi := float32(y[i])
			yil := float32(y[i+halfLen])
			Syy += yil*yil - yi*yi
			if Syy < 1 {
				Syy = 1
			}
		}
	}
	bestPitch = fineBestPitch

	offset := 0
	if bestPitch[0] > 0 && bestPitch[0] < halfPitch-1 {
		a := xcorr[bestPitch[0]-1]
		b := xcorr[bestPitch[0]]
		c := xcorr[bestPitch[0]+1]
		if (c - a) > 0.7*(b-a) {
			offset = 1
		} else if (a - c) > 0.7*(b-c) {
			offset = -1
		}
	}
	return 2*bestPitch[0] - offset
}

func findBestPitchF32(xcorr []float32, y []float32, length, maxPitch int, bestPitch *[2]int) {
	Syy := float32(1)
	bestNum := [2]float32{-1, -1}
	bestDen := [2]float32{0, 0}
	bestPitch[0] = 0
	bestPitch[1] = 1
	_ = y[length+maxPitch-1]
	_ = xcorr[maxPitch-1]
	for j := range length {
		Syy += y[j] * y[j]
	}
	const xcorrScale = float32(1e-12)
	for i := range maxPitch {
		if xv := xcorr[i]; xv > 0 {
			xcorr16 := xv * xcorrScale
			num := xcorr16 * xcorr16
			if num*bestDen[1] > bestNum[1]*Syy {
				if num*bestDen[0] > bestNum[0]*Syy {
					bestNum[1] = bestNum[0]
					bestDen[1] = bestDen[0]
					bestPitch[1] = bestPitch[0]
					bestNum[0] = num
					bestDen[0] = Syy
					bestPitch[0] = i
				} else {
					bestNum[1] = num
					bestDen[1] = Syy
					bestPitch[1] = i
				}
			}
		}
		yi := y[i]
		yil := y[i+length]
		Syy += yil*yil - yi*yi
		if Syy < 1 {
			Syy = 1
		}
	}
}

type pitchSearchRange struct {
	lo int
	hi int
}

const pitchSearchXcorrScale = float32(1e-12)

func normalizePitchSearchRanges(a, b pitchSearchRange) [2]pitchSearchRange {
	if a.hi < a.lo {
		a = pitchSearchRange{lo: 1, hi: 0}
	}
	if b.hi < b.lo {
		b = pitchSearchRange{lo: 1, hi: 0}
	}
	if a.hi < a.lo {
		return [2]pitchSearchRange{b, {lo: 1, hi: 0}}
	}
	if b.hi < b.lo {
		return [2]pitchSearchRange{a, {lo: 1, hi: 0}}
	}
	if b.lo < a.lo {
		a, b = b, a
	}
	if b.lo <= a.hi+1 {
		if b.hi > a.hi {
			a.hi = b.hi
		}
		return [2]pitchSearchRange{a, {lo: 1, hi: 0}}
	}
	return [2]pitchSearchRange{a, b}
}

func pitchSearchFineRanges(bestPitch [2]int, halfPitch int) [2]pitchSearchRange {
	p0 := 2 * bestPitch[0]
	p1 := 2 * bestPitch[1]
	l0 := max(0, p0-2)
	h0 := min(halfPitch-1, p0+2)
	l1 := max(0, p1-2)
	h1 := min(halfPitch-1, p1+2)
	return normalizePitchSearchRanges(
		pitchSearchRange{lo: l0, hi: h0},
		pitchSearchRange{lo: l1, hi: h1},
	)
}

func findBestPitchInRangesF32(xcorr []float32, y []float32, length int, ranges [2]pitchSearchRange, bestPitch *[2]int) {
	Syy := float32(1)
	bestNum := [2]float32{-1, -1}
	bestDen := [2]float32{0, 0}
	bestPitch[0] = 0
	bestPitch[1] = 1
	for j := range length {
		Syy += y[j] * y[j]
	}
	i := 0
	for _, r := range ranges {
		if r.hi < r.lo {
			continue
		}
		for ; i < r.lo; i++ {
			yi := y[i]
			yil := y[i+length]
			Syy += yil*yil - yi*yi
			if Syy < 1 {
				Syy = 1
			}
		}
		for ; i <= r.hi; i++ {
			if xv := xcorr[i]; xv > 0 {
				xcorr16 := xv * pitchSearchXcorrScale
				num := xcorr16 * xcorr16
				if num*bestDen[1] > bestNum[1]*Syy {
					if num*bestDen[0] > bestNum[0]*Syy {
						bestNum[1] = bestNum[0]
						bestDen[1] = bestDen[0]
						bestPitch[1] = bestPitch[0]
						bestNum[0] = num
						bestDen[0] = Syy
						bestPitch[0] = i
					} else {
						bestNum[1] = num
						bestDen[1] = Syy
						bestPitch[1] = i
					}
				}
			}
			yi := y[i]
			yil := y[i+length]
			Syy += yil*yil - yi*yi
			if Syy < 1 {
				Syy = 1
			}
		}
	}
}

func removeDoubling(x []float32, maxPeriod, minPeriod, N int, T0 *int, prevPeriod int, prevGain float32, scratch *encoderScratch) float32 {
	minPeriod0 := minPeriod
	maxPeriod >>= 1
	minPeriod >>= 1
	*T0 >>= 1
	prevPeriod >>= 1
	N >>= 1
	if maxPeriod <= 0 || N <= 0 {
		return 0
	}

	xBase := x
	if *T0 >= maxPeriod {
		*T0 = maxPeriod - 1
	}
	T0val := *T0
	x0 := xBase[maxPeriod:]
	xx, xy := prefilterDualInnerProdF32(x0, x0, xBase[maxPeriod-T0val:maxPeriod-T0val+N], N)

	yyLookup := ensureFloat32Slice(&scratch.prefilterYYLookup, maxPeriod+1)
	yy := xx
	yyLookup[0] = yy
	// Hoist the two descending input windows into fixed-length slices so the
	// per-iteration index is provably in range, dropping three bounds checks
	// per iteration on this maxPeriod-long critical loop. Bit-exact.
	v1s := xBase[:maxPeriod]
	v2s := xBase[N : N+maxPeriod]
	yl := yyLookup[:maxPeriod+1]
	// idx descends (== i ascending) so the loop counter is directly provable
	// in [0,maxPeriod), preserving the exact yy accumulation order.
	for idx := maxPeriod - 1; idx >= 0; idx-- {
		v1 := v1s[idx]
		v2 := v2s[idx]
		yy += v1 * v1
		yy -= v2 * v2
		yl[maxPeriod-idx] = maxFloat32(0, yy)
	}

	yy = yyLookup[T0val]
	bestXY := xy
	bestYY := yy
	g := computePitchGain(xy, xx, yy)
	g0 := g
	T := T0val

	for k := 2; k <= 15; k++ {
		T1 := (2*T0val + k) / (2 * k)
		if T1 < minPeriod {
			break
		}
		var T1b int
		if k == 2 {
			if T1+T0val > maxPeriod {
				T1b = T0val
			} else {
				T1b = T0val + T1
			}
		} else {
			T1b = (2*secondCheck[k]*T0val + k) / (2 * k)
		}
		xy1, xy2 := prefilterDualInnerProdF32(x0, xBase[maxPeriod-T1:maxPeriod-T1+N], xBase[maxPeriod-T1b:maxPeriod-T1b+N], N)
		xy = float32(0.5) * (xy1 + xy2)
		yy = float32(0.5) * (yyLookup[T1] + yyLookup[T1b])
		g1 := computePitchGain(xy, xx, yy)
		cont := float32(0)
		if util.Abs(T1-prevPeriod) <= 1 {
			cont = prevGain
		} else if util.Abs(T1-prevPeriod) <= 2 && 5*k*k < T0val {
			cont = float32(0.5) * prevGain
		}
		thresh := maxFloat32(float32(0.3), float32(0.7)*g0-cont)
		if T1 < 3*minPeriod {
			thresh = maxFloat32(float32(0.4), float32(0.85)*g0-cont)
		} else if T1 < 2*minPeriod {
			thresh = maxFloat32(float32(0.5), float32(0.9)*g0-cont)
		}
		if g1 > thresh {
			bestXY = xy
			bestYY = yy
			T = T1
			g = g1
		}
	}

	if bestXY < 0 {
		bestXY = 0
	}
	pg := g
	if bestYY > bestXY {
		pg = bestXY / noFMA32Add(bestYY, 1)
		if pg > g {
			pg = g
		}
	}

	prev := innerProdFloat32(x0, xBase[maxPeriod-(T-1):maxPeriod-(T-1)+N], N)
	mid := innerProdFloat32(x0, xBase[maxPeriod-T:maxPeriod-T+N], N)
	next := innerProdFloat32(x0, xBase[maxPeriod-(T+1):maxPeriod-(T+1)+N], N)
	xcorr := [3]float32{prev, mid, next}
	offset := 0
	if (xcorr[2] - xcorr[0]) > float32(0.7)*(xcorr[1]-xcorr[0]) {
		offset = 1
	} else if (xcorr[0] - xcorr[2]) > float32(0.7)*(xcorr[1]-xcorr[2]) {
		offset = -1
	}
	*T0 = max(2*T+offset, minPeriod0)
	return pg
}

func maxFloat32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func prefilterDualInnerProdF32(x, y1, y2 []float32, length int) (float32, float32) {
	if length <= 0 {
		return 0, 0
	}
	_ = x[length-1]
	_ = y1[length-1]
	_ = y2[length-1]
	if libopusFloatInnerProdUsesNeonOrder {
		return prefilterDualInnerProdF32NeonOrder(x, y1, y2, length)
	}
	if libopusFloatInnerProdUsesSSEOrder {
		return prefilterDualInnerProdF32SSEOrder(x, y1, y2, length)
	}
	sum1 := float32(0)
	sum2 := float32(0)
	for i := range length {
		xi := x[i]
		sum1 += xi * y1[i]
		sum2 += xi * y2[i]
	}
	return sum1, sum2
}

func prefilterDualInnerProdF32SSEOrder(x, y1, y2 []float32, length int) (float32, float32) {
	var acc1 [4]float32
	var acc2 [4]float32
	i := 0
	for ; i < length-3; i += 4 {
		x0 := x[i]
		acc1[0] = noFMA32Add(acc1[0], noFMA32Mul(x0, y1[i]))
		acc2[0] = noFMA32Add(acc2[0], noFMA32Mul(x0, y2[i]))
		x1 := x[i+1]
		acc1[1] = noFMA32Add(acc1[1], noFMA32Mul(x1, y1[i+1]))
		acc2[1] = noFMA32Add(acc2[1], noFMA32Mul(x1, y2[i+1]))
		x2 := x[i+2]
		acc1[2] = noFMA32Add(acc1[2], noFMA32Mul(x2, y1[i+2]))
		acc2[2] = noFMA32Add(acc2[2], noFMA32Mul(x2, y2[i+2]))
		x3 := x[i+3]
		acc1[3] = noFMA32Add(acc1[3], noFMA32Mul(x3, y1[i+3]))
		acc2[3] = noFMA32Add(acc2[3], noFMA32Mul(x3, y2[i+3]))
	}
	sum1 := noFMA32Add(noFMA32Add(acc1[0], acc1[2]), noFMA32Add(acc1[1], acc1[3]))
	sum2 := noFMA32Add(noFMA32Add(acc2[0], acc2[2]), noFMA32Add(acc2[1], acc2[3]))
	for ; i < length; i++ {
		xi := x[i]
		sum1 = noFMA32Add(sum1, noFMA32Mul(xi, y1[i]))
		sum2 = noFMA32Add(sum2, noFMA32Mul(xi, y2[i]))
	}
	return sum1, sum2
}

// prefilterDualInnerProdF32NeonOrder reproduces libopus
// arm/pitch_neon_intr.c dual_inner_prod_neon: two 4-lane vfmaq_f32 accumulators
// over 8-element groups, a 4-element tail, the (acc0+acc2)+(acc1+acc3)
// reductions, and a fused multiply-add scalar tail. prefilterDualInnerProdAsm
// implements this in NEON asm on arm64 and a bit-identical math.FMA fallback
// under the purego tag.
func prefilterDualInnerProdF32NeonOrder(x, y1, y2 []float32, length int) (float32, float32) {
	return prefilterDualInnerProdAsm(x, y1, y2, length)
}

func computePitchGain(xy, xx, yy float32) float32 {
	if xy == 0 || xx == 0 || yy == 0 {
		return 0
	}
	den := noFMA32Add(1, noFMA32Mul(xx, yy))
	return xy / opusmath.SqrtF32(den)
}

func celtFIR5F32(x []float32, num [5]float32) {
	n0 := num[0]
	n1 := num[1]
	n2 := num[2]
	n3 := num[3]
	n4 := num[4]
	mem0 := float32(0)
	mem1 := float32(0)
	mem2 := float32(0)
	mem3 := float32(0)
	mem4 := float32(0)
	i := 0
	for ; i+1 < len(x); i += 2 {
		x0 := x[i]
		sum0 := x0 + n0*mem0 + n1*mem1 + n2*mem2 + n3*mem3 + n4*mem4
		x1 := x[i+1]
		sum1 := x1 + n0*x0 + n1*mem0 + n2*mem1 + n3*mem2 + n4*mem3
		x[i] = sum0
		x[i+1] = sum1
		mem4 = mem2
		mem3 = mem1
		mem2 = mem0
		mem1 = x0
		mem0 = x1
	}
	for ; i < len(x); i++ {
		xi := x[i]
		sum := xi + n0*mem0 + n1*mem1 + n2*mem2 + n3*mem3 + n4*mem4
		mem4 = mem3
		mem3 = mem2
		mem2 = mem1
		mem1 = mem0
		mem0 = xi
		x[i] = sum
	}
}

func lpcFromAutocorr32(ac [5]float32) [4]float32 {
	var lpc [4]float32
	plcLPCFromAutocorr(ac[:], lpc[:])
	return lpc
}

func pitchAutocorr5F32(lp []float32, length int, ac *[5]float32) {
	fastN := max(length-4, 0)
	pitchXCorrFloat32(lp, lp, ac[:], fastN, 5)
	for lag := 0; lag <= 4; lag++ {
		tail := float32(0)
		for i := lag + fastN; i < length; i++ {
			tail += lp[i] * lp[i-lag]
		}
		ac[lag] += tail
	}
}

// prefilterInnerProd and prefilterDualInnerProd are implemented in:
//   prefilter_innerprod_asm.go + prefilter_innerprod_{arm64,amd64}.s  (SIMD path)
//   prefilter_innerprod_default.go                                     (Go fallback)

var secondCheck = [16]int{0, 0, 3, 2, 3, 2, 5, 2, 3, 2, 3, 2, 5, 2, 3, 2}

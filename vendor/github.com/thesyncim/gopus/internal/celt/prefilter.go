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
// This mirrors libopus run_prefilter() in celt_encoder.c. in is
// celt_encode_with_ec's planar buffer: channel c occupies
// in[c*(frameSize+overlap):(c+1)*(frameSize+overlap)] with the pre-emphasized
// frame after its overlap head. run_prefilter filters the frame in place,
// loads the head from st->in_mem and saves the filtered tail back to it.
func (e *Encoder) runPrefilter(in []float32, frameSize int, tapset int, enabled bool, tfEstimate float32, nbAvailableBytes int, toneFreq, toneishness, maxPitchRatio float32) prefilterResult {
	// celt_encode_with_ec enables the pitch prefilter only when start == 0.
	// Hybrid frames still run the disabled transition to update filter history.
	enabled = enabled && !e.IsHybrid()
	result := prefilterResult{on: false, pitch: combFilterMinPeriod, qg: 0, tapset: tapset, gain: 0}
	channels := int(e.channels)
	overlap := min(e.analysisOverlap(), frameSize)
	stride := frameSize + overlap
	if channels <= 0 || frameSize <= 0 || len(in) < channels*stride {
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
	e.recordPitchControls(frameSize, channels, enabled, e.complexity, maxPeriod, minPeriod,
		tfEstimate, toneFreq, toneishness, maxPitchRatio)
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
		e.recordEncodePrefilterNoopTrace(nil, in, frameSize, channels, overlap, 0,
			prevPeriod, combFilterMinPeriod, 0, 0, prevTapset, tapset)
		e.updatePrefilterNoopStateFromIn(in, frameSize, channels, overlap)
		e.prefilterPeriod = combFilterMinPeriod
		e.prefilterGain = 0
		e.prefilterTapset = tapset
		result.tapset = tapset
		return result
	}
	perChanLen := maxPeriod + frameSize
	pre := ensureSigSliceNoClear(&e.scratch.prefilterPre, perChanLen*channels)

	for ch := range channels {
		preCh := pre[ch*perChanLen : (ch+1)*perChanLen]
		// celtSig is a float32 alias, so both copies are plain memmoves.
		copy(preCh[:maxPeriod], e.prefilterMem[ch*maxPeriod:(ch+1)*maxPeriod])
		copy(preCh[maxPeriod:], in[ch*stride+overlap:ch*stride+overlap+frameSize])
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
		e.runPrefilterPitchDownsample(pre, pitchBuf, pitchBufLen, channels, perChanLen, 2)
		maxPitch := max(maxPeriod-3*minPeriod, 1)
		searchOut := e.runPrefilterPitchSearch(pitchBuf, maxPeriod>>1, frameSize, maxPitch)
		pitchIndex = searchOut
		pitchIndex = maxPeriod - pitchIndex
		gain1 = e.runPrefilterRemoveDoubling(pitchBuf, maxPeriod, minPeriod, frameSize, &pitchIndex)
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

	if gain1 == 0 && e.prefilterGain == 0 {
		e.recordEncodePrefilterNoopTrace(pre, in, frameSize, channels, overlap, perChanLen,
			prevPeriod, pitchIndex, -e.prefilterGain, -gain1, prevTapset, tapset)
		e.updatePrefilterNoopState(pre, in, perChanLen, frameSize, channels, overlap)
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
	window := e.scratch.modeWindow(overlap)

	for ch := range channels {
		preCh := pre[ch*perChanLen : (ch+1)*perChanLen]
		outCh := out[ch*perChanLen : (ch+1)*perChanLen]
		if offset > 0 {
			traceCall := e.beginEncodePrefilterCombTrace(ch, outCh, preCh, maxPeriod, prevPeriod, prevPeriod, offset,
				-e.prefilterGain, -e.prefilterGain, prevTapset, prevTapset, nil, 0)
			combFilterWithInputSig(outCh, preCh, maxPeriod, prevPeriod, prevPeriod, offset, -e.prefilterGain, -e.prefilterGain, prevTapset, prevTapset, nil, 0)
			e.finishEncodePrefilterCombTrace(traceCall, outCh, maxPeriod, offset)
		}
		start := maxPeriod + offset
		n := frameSize - offset
		traceCall := e.beginEncodePrefilterCombTrace(ch, outCh, preCh, start, prevPeriod, pitchIndex, n,
			-e.prefilterGain, -gain1, prevTapset, tapset, window, overlap)
		combFilterWithInputSig(outCh, preCh, start, prevPeriod, pitchIndex, n, -e.prefilterGain, -gain1, prevTapset, tapset, window, overlap)
		e.finishEncodePrefilterCombTrace(traceCall, outCh, start, n)
	}
	// before[c] and after[c] are run_prefilter's serial ABS32 sums over the
	// input and the comb-filtered output.
	var before, after [2]opusVal32
	preSub := func(ch int) []celtSig { return pre[ch*perChanLen+maxPeriod : ch*perChanLen+maxPeriod+frameSize] }
	outSub := func(ch int) []celtSig { return out[ch*perChanLen+maxPeriod : ch*perChanLen+maxPeriod+frameSize] }
	if channels == 2 {
		before[0], before[1], after[0], after[1] = absSumSig4(preSub(0), preSub(1), outSub(0), outSub(1))
	} else {
		before[0], after[0] = absSumSig2(preSub(0), outSub(0))
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
			start := maxPeriod + offset
			traceCall := e.beginEncodePrefilterCombTrace(ch, outCh, preCh, start, prevPeriod, pitchIndex, overlap,
				-e.prefilterGain, 0, prevTapset, tapset, window, overlap)
			combFilterWithInputSig(outCh, preCh, start, prevPeriod, pitchIndex, overlap, -e.prefilterGain, 0, prevTapset, tapset, window, overlap)
			e.finishEncodePrefilterCombTrace(traceCall, outCh, start, overlap)
		}
		gain1 = 0
		pfOn = false
		qg = 0
	}

	for ch := range channels {
		outCh := out[ch*perChanLen+maxPeriod : ch*perChanLen+maxPeriod+frameSize]
		copySigToFloat32(in[ch*stride+overlap:ch*stride+overlap+frameSize], outCh)
	}
	e.savePrefilterMem(pre, perChanLen, frameSize, channels)
	e.swapPrefilterInMem(in, frameSize, channels, overlap)

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

// updatePrefilterNoopState is run_prefilter's state update when both the old
// and the new gain are zero: the comb filter leaves the frame in in unchanged,
// so only prefilter_mem and in_mem advance.
func (e *Encoder) updatePrefilterNoopState(pre []celtSig, in []float32, perChanLen, frameSize, channels, overlap int) {
	e.savePrefilterMem(pre, perChanLen, frameSize, channels)
	e.swapPrefilterInMem(in, frameSize, channels, overlap)
}

// updatePrefilterNoopStateFromIn is updatePrefilterNoopState before pre is
// built: prefilter_mem advances straight from the frame in in.
func (e *Encoder) updatePrefilterNoopStateFromIn(in []float32, frameSize, channels, overlap int) {
	maxPeriod := e.combMaxPeriod()
	stride := frameSize + overlap
	for ch := range channels {
		mem := e.prefilterMem[ch*maxPeriod : (ch+1)*maxPeriod]
		frame := in[ch*stride+overlap : ch*stride+overlap+frameSize]
		if frameSize > maxPeriod {
			copyFloat32ToSig(mem, frame[frameSize-maxPeriod:])
		} else {
			copy(mem, mem[frameSize:])
			copyFloat32ToSig(mem[maxPeriod-frameSize:], frame)
		}
	}
	e.swapPrefilterInMem(in, frameSize, channels, overlap)
}

// savePrefilterMem is run_prefilter's prefilter_mem update from pre: the last
// max_period samples of the unfiltered history and frame.
func (e *Encoder) savePrefilterMem(pre []celtSig, perChanLen, frameSize, channels int) {
	maxPeriod := e.combMaxPeriod()
	for ch := range channels {
		preCh := pre[ch*perChanLen : (ch+1)*perChanLen]
		mem := e.prefilterMem[ch*maxPeriod : (ch+1)*maxPeriod]
		if frameSize > maxPeriod {
			copy(mem, preCh[frameSize:frameSize+maxPeriod])
		} else {
			copy(mem, mem[frameSize:])
			copy(mem[maxPeriod-frameSize:], preCh[maxPeriod:maxPeriod+frameSize])
		}
	}
}

// swapPrefilterInMem is run_prefilter's in_mem exchange: each channel's
// overlap head in in takes the previous filtered tail from st->in_mem, and
// st->in_mem keeps this frame's filtered tail in[c*(N+overlap)+N:].
func (e *Encoder) swapPrefilterInMem(in []float32, frameSize, channels, overlap int) {
	if overlap <= 0 {
		return
	}
	if need := channels * overlap; len(e.overlapBuffer) < need {
		newBuf := make([]celtSig, need)
		copy(newBuf, e.overlapBuffer)
		e.overlapBuffer = newBuf
	}
	stride := frameSize + overlap
	for ch := range channels {
		mem := e.overlapBuffer[ch*overlap : (ch+1)*overlap]
		inCh := in[ch*stride : (ch+1)*stride]
		copySigToFloat32(inCh[:overlap], mem)
		copyFloat32ToSig(mem, inCh[frameSize:])
	}
}

type pitchDownsampleIntermediateCapture struct {
	Decimated          []float32
	RawAutocorrelation [5]float32
	LPCInput           [5]float32
	LPC                [4]float32
	Captured           uint32
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
		switch channels {
		case 1:
			xLP[0] = firQuarter*float32(x[1]) + firHalf*float32(x[0])
			if length > 1 && len(x) >= 2*length {
				pitchDownsample2(xLP[:length], x[:2*length], nil)
			}
		case 2:
			chStride := len(x) / 2
			x0 := x[:chStride]
			x1 := x[chStride:]
			v0 := firQuarter*float32(x0[1]) + firHalf*float32(x0[0])
			v1 := firQuarter*float32(x1[1]) + firHalf*float32(x1[0])
			xLP[0] = v0 + v1
			if length > 1 && len(x0) >= 2*length && len(x1) >= 2*length {
				pitchDownsample2(xLP[:length], x0[:2*length], x1[:2*length])
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
	if pitchDownsampleTraceCaptureEnabled {
		recordPitchDownsampleDecimated(x, xLP, length, channels, factor)
	}

	var ac [5]float32
	pitchAutocorr5F32(xLP[:length], length, &ac)
	if pitchDownsampleTraceCaptureEnabled {
		recordPitchDownsampleAutocorrelation(x, xLP, length, channels, factor, ac)
	}

	applyCELTPitchLagWindow32(ac[:], 4)
	if pitchDownsampleTraceCaptureEnabled {
		recordPitchDownsampleLPCInput(x, xLP, length, channels, factor, ac)
	}

	lpc := lpcFromAutocorr32(ac)
	if pitchDownsampleTraceCaptureEnabled {
		recordPitchDownsampleLPC(x, xLP, length, channels, factor, lpc)
	}
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
		Syy = pitchSearchAddSquare(Syy, yj)
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
			Syy = pitchSearchSlideSyy(Syy, yil, yi)
			if Syy < 1 {
				Syy = 1
			}
		}
		n := r.hi - r.lo + 1
		if libopusFloatInnerProdUsesNeonOrder {
			// libopus pitch.c calls celt_inner_prod for each fine candidate;
			// its NEON lane reduction differs from the four-lag xcorr kernel.
			for j := r.lo; j <= r.hi; j++ {
				xcorr[j] = innerProdFloat32(xLP, y[j:], halfLen)
			}
		} else {
			pitchXCorrFloat32Quality(xLP, y[r.lo:], xcorr[r.lo:], halfLen, n)
		}
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
			Syy = pitchSearchSlideSyy(Syy, yil, yi)
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
		Syy = pitchSearchAddSquare(Syy, y[j])
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
		Syy = pitchSearchSlideSyy(Syy, yil, yi)
		if Syy < 1 {
			Syy = 1
		}
	}
}

// pitchSearchAddSquare and pitchSearchSlideSyy match the float recurrence in
// libopus celt/pitch.c find_best_pitch(). The selected ARM scalar C build
// contracts Syy additions; the NEON build reduces vectorized square groups in
// input order, then contracts only its scalar tail.
func pitchSearchAddSquare(sum, value float32) float32 {
	if libopusPitchSearchUsesFMA && !libopusFloatInnerProdUsesNeonOrder {
		return opusmath.FMA32(value, value, sum)
	}
	return sum + value*value
}

func pitchSearchSlideSyy(sum, entering, leaving float32) float32 {
	// celt/pitch.c find_best_pitch() forms Syy + (entering² - leaving²).
	if libopusPitchSearchUsesFMA {
		// The selected ARM C variants contract the entering square with the
		// rounded negative leaving square, then add the rounded delta to Syy.
		delta := opusmath.FMA32(entering, entering, -noFMA32Mul(leaving, leaving))
		return noFMA32Add(sum, delta)
	}
	delta := noFMA32Sub(noFMA32Mul(entering, entering), noFMA32Mul(leaving, leaving))
	return noFMA32Add(sum, delta)
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
	if removeDoublingMathTraceCaptureEnabled {
		recordRemoveDoublingDual(xx, xy)
	}

	// yy_lookup[i] is a running sum, and the search below reads it only at
	// T0, T1 <= T0 and T1b, which is at most T0+T1 for k == 2 and below T0
	// after that. The prefix up to that bound holds the same values as the
	// full maxperiod table.
	limit := min(T0val+(2*T0val+2)/4, maxPeriod)
	yyLookup := ensureFloat32Slice(&scratch.prefilterYYLookup, maxPeriod+1)
	yy := xx
	yyLookup[0] = yy
	// Hoist the two descending input windows into fixed-length slices so the
	// per-iteration index is provably in range. Bit-exact.
	v1s := xBase[maxPeriod-limit : maxPeriod]
	v2s := xBase[N+maxPeriod-limit : N+maxPeriod]
	yl := yyLookup[:limit+1]
	// idx descends (== i ascending) so the loop counter is directly provable
	// in [0,limit), preserving the exact yy accumulation order.
	for idx := limit - 1; idx >= 0; idx-- {
		v1 := v1s[idx]
		v2 := v2s[idx]
		yy = removeDoublingYYUpdate32(yy, v1, v2)
		yl[limit-idx] = maxFloat32(0, yy)
		if removeDoublingMathTraceCaptureEnabled {
			recordRemoveDoublingYY(limit-idx, v1, v2, yy, yl[limit-idx])
		}
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
		if removeDoublingMathTraceCaptureEnabled {
			recordRemoveDoublingDual(xy1, xy2)
		}
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
	// celt/pitch.c sets pg to Q15ONE when best_yy <= best_xy, then clamps
	// pg to the selected gain in either branch.
	pg := float32(1)
	if bestYY > bestXY {
		pg = bestXY / noFMA32Add(bestYY, 1)
	}
	if pg > g {
		pg = g
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

func prefilterDualInnerProdF32SSEOrderScalar(x, y1, y2 []float32, length int) (float32, float32) {
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
// reductions, and a fused multiply-add scalar tail. The arm64 SIMD Go kernel
// uses archsimd; scalar Go builds preserve the same operation order.
func prefilterDualInnerProdF32NeonOrder(x, y1, y2 []float32, length int) (float32, float32) {
	return prefilterDualInnerProdAsm(x, y1, y2, length)
}

func computePitchGain(xy, xx, yy float32) float32 {
	if pitchGainZeroInputReturnsZero && (xy == 0 || xx == 0 || yy == 0) {
		if removeDoublingMathTraceCaptureEnabled {
			recordRemoveDoublingGain(xy, xx, yy, 0, 0, 0)
		}
		return 0
	}
	den := pitchGainDenominator32(xx, yy)
	root := opusmath.SqrtF32(den)
	gain := xy / root
	if removeDoublingMathTraceCaptureEnabled {
		recordRemoveDoublingGain(xy, xx, yy, den, root, gain)
	}
	return gain
}

// pitchDownsample2Scalar computes outputs [start, len(dst)) of the factor-2
// pitch_downsample() decimation: dst[i] is 0.25*x[2i-1] + 0.25*x[2i+1] +
// 0.5*x[2i] of x0, plus the same sum of x1 when x1 is not nil, in the C
// operation order. x0 and x1 hold 2*len(dst) samples and start is at least 1.
func pitchDownsample2Scalar(dst, x0, x1 []float32, start int) {
	const (
		firQuarter = float32(0.25)
		firHalf    = float32(0.5)
	)
	n := len(dst)
	if start < 1 || start >= n || len(x0) < 2*n || (x1 != nil && len(x1) < 2*n) {
		return
	}
	// w[0], w[1] and w[2] are x[2i-1], x[2i] and x[2i+1] of output i.
	w0 := x0[2*start-1 : 2*n]
	dst = dst[start:]
	if x1 == nil {
		// 4-output unroll: consecutive outputs share x[2i+1] = x[2(i+1)-1],
		// reducing loads from 12 to 9 per 4 outputs.
		for len(dst) >= 4 && len(w0) >= 9 {
			a0, a1, a2, a3, a4, a5, a6, a7, a8 := w0[0], w0[1], w0[2], w0[3], w0[4], w0[5], w0[6], w0[7], w0[8]
			dst[0] = firQuarter*float32(a0) + firQuarter*float32(a2) + firHalf*float32(a1)
			dst[1] = firQuarter*float32(a2) + firQuarter*float32(a4) + firHalf*float32(a3)
			dst[2] = firQuarter*float32(a4) + firQuarter*float32(a6) + firHalf*float32(a5)
			dst[3] = firQuarter*float32(a6) + firQuarter*float32(a8) + firHalf*float32(a7)
			w0 = w0[8:]
			dst = dst[4:]
		}
		for len(dst) > 0 && len(w0) >= 3 {
			dst[0] = firQuarter*float32(w0[0]) + firQuarter*float32(w0[2]) + firHalf*float32(w0[1])
			w0 = w0[2:]
			dst = dst[1:]
		}
		return
	}
	w1 := x1[2*start-1 : 2*n]
	// 2x unroll: w[2] is shared by the pair, and the channels are independent.
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

// celtFIR5Scalar is libopus celt_fir5() (celt/celt_lpc.c) in the float
// build: each output adds the five taps on the previous inputs, starting from
// zero filter memory, in the C order.
func celtFIR5Scalar(x []float32, num [5]float32) {
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
		tailStart := lag + fastN
		// The paired GCC 13.3 x86-v3 and Darwin ARM64 SIMD callers in
		// celt/celt_lpc.c:_celt_autocorr round four-term vector products
		// before ordered adds and fuse the scalar residual MAC16_16 updates.
		// Target helpers preserve that split and the separate prefix addition.
		if pitchAutocorrRoundFourTermTail && length-tailStart == 4 {
			tail = pitchAutocorrTail4(lp[tailStart:], lp[tailStart-lag:])
		} else if pitchAutocorrUsesFMA32 && length-tailStart < 4 {
			for i := tailStart; i < length; i++ {
				tail = pitchAutocorrMAC32(lp[i], lp[i-lag], tail)
			}
		} else {
			for i := tailStart; i < length; i++ {
				tail += lp[i] * lp[i-lag]
			}
		}
		ac[lag] += tail
	}
}

// prefilterDualInnerProdF32 selects scalar Go by default and archsimd kernels
// under GOEXPERIMENT=simd where the architecture-specific implementation exists.

var secondCheck = [16]int{0, 0, 3, 2, 3, 2, 5, 2, 3, 2, 3, 2, 5, 2, 3, 2}

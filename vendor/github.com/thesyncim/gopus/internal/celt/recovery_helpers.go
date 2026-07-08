package celt

import (
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/plc"
)

const celtPLCLPCOrder = 24

func (d *Decoder) resetPLCCadence(frameSize, channels int) {
	d.plcLossDuration = 0
	d.plcDuration = 0
	d.plcLastFrameType = frameNormal
	d.plcPrefilterAndFoldPending = false
	d.plcPrevLossWasPeriodic = false
	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeCELT, frameSize, channels)
}

func (d *Decoder) beginDecodedPacketPLCState() {
	if d == nil {
		return
	}
	if d.plcLossDuration == 0 {
		d.plcSkip = false
	}
}

func plcFrameIsNeural(frameType int) bool {
	return frameType == framePLCNeural || frameType == frameDRED
}

func (d *Decoder) lastPLCFrameWasPeriodic() bool {
	if d == nil {
		return false
	}
	return d.plcLastFrameType == framePLCPeriodic
}

func (d *Decoder) chooseLostFrameType(start int, allowNeural, allowDRED bool) int {
	currFrameType := framePLCPeriodic
	if d == nil {
		return currFrameType
	}
	if d.plcDuration >= 40 || start != 0 || d.plcSkip {
		currFrameType = framePLCNoise
	}
	if start == 0 && allowNeural && d.complexity >= 5 && d.plcDuration < 80 && !d.plcSkip {
		currFrameType = framePLCNeural
	}
	if start == 0 && allowNeural && allowDRED {
		currFrameType = frameDRED
	}
	return currFrameType
}

func (d *Decoder) finishLostFrame(currFrameType, frameSize int) {
	if d == nil {
		return
	}
	d.accumulatePLCLossDuration(frameSize)
	switch currFrameType {
	case framePLCNoise:
		d.plcSkip = true
	case frameDRED:
		d.plcDuration = 0
		d.plcSkip = false
	}
	d.plcLastFrameType = int32(currFrameType)
	d.plcPrevLossWasPeriodic = currFrameType == framePLCPeriodic
}

func (d *Decoder) applyPendingPLCPrefilterAndFold() {
	if !d.plcPrefilterAndFoldPending {
		return
	}
	// Match libopus cadence: consume the pending fold exactly once.
	d.plcPrefilterAndFoldPending = false

	if d.channels <= 0 || Overlap <= 0 {
		return
	}
	channels := int(d.channels)
	if len(d.plcDecodeMem) < plcDecodeBufferSize*channels {
		return
	}
	d.materializePLCDecodeHistory()
	if len(d.overlapBuffer) < Overlap*channels {
		return
	}

	const history = combFilterHistory
	const segLen = Overlap
	if history <= 0 || segLen <= 0 || plcDecodeBufferSize < history {
		return
	}

	bufLen := history + segLen
	d.scratchPLCFoldSrc = ensureSigSlice(&d.scratchPLCFoldSrc, bufLen)
	d.scratchPLCFoldDst = ensureSigSlice(&d.scratchPLCFoldDst, bufLen)
	window := GetWindowBuffer(segLen)
	half := segLen >> 1

	traceArmed := d.plcStageTrace != nil && d.plcStageTrace.observeFold()

	for ch := range channels {
		hist := d.plcDecodeMem[ch*plcDecodeBufferSize : (ch+1)*plcDecodeBufferSize]
		overlap := d.overlapBuffer[ch*segLen : (ch+1)*segLen]
		src := d.scratchPLCFoldSrc[:bufLen]
		dst := d.scratchPLCFoldDst[:bufLen]

		copy(src[:history], hist[plcDecodeBufferSize-history:])
		copy(src[history:], overlap)

		if traceArmed {
			d.plcStageTrace.captureCombIn(ch, src)
		}

		combFilterWithInputSig(
			dst, src, history,
			int(d.postfilterPeriodOld), int(d.postfilterPeriod), segLen,
			-d.postfilterGainOld, -d.postfilterGain,
			int(d.postfilterTapsetOld), int(d.postfilterTapset),
			nil, 0,
		)

		etmp := dst[history : history+segLen]
		if traceArmed {
			d.plcStageTrace.captureCombOut(ch, etmp)
		}
		for i := range half {
			// Simulate TDAC blending exactly where libopus mutates decode_mem.
			w0 := float32(window[i])
			w1 := float32(window[segLen-1-i])
			x0 := float32(etmp[segLen-1-i])
			x1 := float32(etmp[i])
			overlap[i] = celtSig(mdctFMA32(w0, x0, w1*x1))
		}
	}

	if traceArmed {
		d.plcStageTrace.captureFold(d.overlapBuffer, channels, segLen)
	}
}

func (d *Decoder) accumulatePLCLossDuration(frameSize int) {
	lm := max(GetModeConfig(frameSize).LM, 0)
	if lm > 30 {
		lm = 30
	}
	d.plcLossDuration += 1 << uint(lm)
	if d.plcLossDuration > 10000 {
		d.plcLossDuration = 10000
	}
	d.plcDuration += 1 << uint(lm)
	if d.plcDuration > 10000 {
		d.plcDuration = 10000
	}
}

func (d *Decoder) applyLossEnergySafety(intra bool, start, end, lm int) {
	// Port of libopus celt_decode_with_ec() loss-recovery safety before
	// unquant_coarse_energy(): clamp oldBandE prediction after packet loss.
	if intra || d.plcLossDuration == 0 {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if start >= end {
		return
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 30 {
		lm = 30
	}

	missing := min(10, int(d.plcLossDuration>>uint(lm)))
	safety := celtGLog(0)
	switch lm {
	case 0:
		safety = 1.5
	case 1:
		safety = 0.5
	}

	channels := int(d.channels)
	predStride := d.predStride()
	for c := range channels {
		base := c * predStride
		if base+end > len(d.prevEnergy) || base+end > len(d.prevLogE) || base+end > len(d.prevLogE2) {
			continue
		}
		for i := start; i < end; i++ {
			idx := base + i
			e0 := d.prevEnergy[idx]
			e1 := d.prevLogE[idx]
			e2 := d.prevLogE2[idx]

			maxPrev := e1
			if e2 > maxPrev {
				maxPrev = e2
			}
			if e0 < maxPrev {
				slope := e1 - e0
				halfSlope := celtGLog(0.5) * (e2 - e0)
				if halfSlope > slope {
					slope = halfSlope
				}
				if slope > 2.0 {
					slope = 2.0
				}
				dec := celtGLog(1+missing) * slope
				if dec < 0 {
					dec = 0
				}
				e0 -= dec
				if e0 < -20.0 {
					e0 = -20.0
				}
			} else {
				if e1 < e0 {
					e0 = e1
				}
				if e2 < e0 {
					e0 = e2
				}
			}
			d.prevEnergy[idx] = e0 - safety
		}
	}
}

// DecodeHybridFECPLC generates CELT concealment for hybrid cadence.
// This mirrors the decode_fec behavior where CELT PLC is accumulated on top of
// SILK LBRR, and it is also used for the 5 ms hybrid->CELT transition decode.
// Decoder-side postfilter/de-emphasis ordering matches libopus.
func (d *Decoder) DecodeHybridFECPLC(frameSize int) ([]float32, error) {
	if frameSize != 240 && frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidFrameSize
	}

	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	_ = d.plcState.RecordLoss()
	prevLossDuration := d.plcLossDuration
	d.accumulatePLCLossDuration(frameSize)
	d.plcPrevLossWasPeriodic = false
	d.plcPrefilterAndFoldPending = false
	d.plcLastFrameType = framePLCNoise
	d.plcSkip = true

	channels := int(d.channels)
	outLen := frameSize * channels
	d.scratchPLCF32 = ensureFloat32Slice(&d.scratchPLCF32, outLen)
	decayDB := celtGLog(0.5)
	if prevLossDuration == 0 {
		decayDB = 1.5
	}
	d.ensureBackgroundEnergyState()
	concealEnergy := ensureGLogSlice(&d.scratchPrevEnergyGLog, len(d.prevEnergy))
	copy(concealEnergy, d.prevEnergy)

	// Match libopus celt_decode_lost() noise PLC cadence: in hybrid mode,
	// only the coded CELT band range [start,end) gets decayed/floored.
	mode := GetModeConfig(frameSize)
	start := HybridCELTStartBand
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < start {
		end = start
	}
	predStride := d.predStride()
	for c := range channels {
		base := c * predStride
		for band := start; band < end; band++ {
			idx := base + band
			e := d.prevEnergy[idx] - decayDB
			if bg := d.backgroundEnergy[idx]; bg > e {
				e = bg
			}
			concealEnergy[idx] = e
		}
	}

	seed := d.rng
	if d.channels == 2 {
		d.scratchPLCHybridNormL = ensureNormSlice(&d.scratchPLCHybridNormL, frameSize)
		d.scratchPLCHybridNormR = ensureNormSlice(&d.scratchPLCHybridNormR, frameSize)
		coeffsL := d.scratchPLCHybridNormL[:frameSize]
		coeffsR := d.scratchPLCHybridNormR[:frameSize]
		clear(coeffsL)
		clear(coeffsR)
		fillHybridPLCNoiseCoeffs(coeffsL, frameSize, start, end, &seed)
		fillHybridPLCNoiseCoeffs(coeffsR, frameSize, start, end, &seed)
		denormalizeNormCoeffsDownsample(coeffsL, concealEnergy[:predStride], end, frameSize, d.downsampleFactor())
		denormalizeNormCoeffsDownsample(coeffsR, concealEnergy[predStride:], end, frameSize, d.downsampleFactor())
		samples := d.SynthesizeStereoFloat32(coeffsL, coeffsR, false, 1)
		copy(d.scratchPLCF32[:outLen], samples[:min(outLen, len(samples))])
	} else {
		d.scratchPLCHybridNormL = ensureNormSlice(&d.scratchPLCHybridNormL, frameSize)
		coeffs := d.scratchPLCHybridNormL[:frameSize]
		clear(coeffs)
		fillHybridPLCNoiseCoeffs(coeffs, frameSize, start, end, &seed)
		denormalizeNormCoeffsDownsample(coeffs, concealEnergy[:predStride], end, frameSize, d.downsampleFactor())
		samples := d.SynthesizeFloat32(coeffs, false, 1)
		copy(d.scratchPLCF32[:outLen], samples[:min(outLen, len(samples))])
	}
	d.setPrevEnergyGLog(concealEnergy)
	d.rng = seed

	d.applyPostfilterFloat32(d.scratchPLCF32[:outLen], frameSize, mode.LM, int(d.postfilterPeriod), d.postfilterGain, int(d.postfilterTapset))
	d.applyDeemphasisAndScaleFloat32(d.scratchPLCF32[:outLen], 1.0/32768.0)

	return d.scratchPLCF32[:outLen], nil
}

func fillHybridPLCNoiseCoeffs(coeffs []celtNorm, frameSize, startBand, endBand int, seed *uint32) {
	if len(coeffs) < frameSize || frameSize <= 0 {
		return
	}
	if startBand < 0 {
		startBand = 0
	}
	if endBand > MaxBands {
		endBand = MaxBands
	}
	if endBand < startBand {
		endBand = startBand
	}

	for band := startBand; band < endBand; band++ {
		start := ScaledBandStart(band, frameSize)
		end := ScaledBandStart(band+1, frameSize)
		if start < 0 {
			start = 0
		}
		if end > frameSize {
			end = frameSize
		}
		if start >= end {
			continue
		}
		for i := start; i < end; i++ {
			*seed = *seed*1664525 + 1013904223
			coeffs[i] = celtNorm(int32(*seed) >> 20)
		}
		normalizeNormVectorInPlace(coeffs[start:end])
	}
}

// decodePLC generates concealment audio for a lost CELT packet.
func (d *Decoder) decodePLC(frameSize int) ([]float32, error) {
	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Keep PLC loss cadence bookkeeping.
	prevLossDuration := d.plcLossDuration
	_ = d.plcState.RecordLoss()
	lossCount := d.plcState.LostCount()

	// Ensure scratch buffer is large enough
	channels := int(d.channels)
	outLen := frameSize * channels
	plcLen := (frameSize + Overlap) * channels
	d.scratchPLC = ensureFloat32Slice(&d.scratchPLC, plcLen)

	currFrameType := d.chooseLostFrameType(0, false, false)

	// Match libopus decode_lost() mode cadence: favor periodic concealment in the
	// early loss window and fall back to noise-based concealment when unavailable.
	if currFrameType == framePLCPeriodic &&
		d.concealPeriodicPLCLimited(d.scratchPLC[:plcLen], frameSize, lossCount, d.lastPLCFrameWasPeriodic(), true) {
		d.finishLostFrame(framePLCPeriodic, frameSize)
		d.plcPrefilterAndFoldPending = true
		d.updatePLCOverlapBuffer(d.scratchPLC[:plcLen], frameSize)
		if len(d.directOutPCM) >= outLen {
			d.applyDeemphasisAndScaleToFloat32(d.directOutPCM[:outLen], d.scratchPLC[:outLen], 1.0/32768.0)
			return d.scratchPLC[:outLen], nil
		}
		d.applyDeemphasisAndScale(d.scratchPLC[:outLen], 1.0/32768.0)
		return d.scratchPLC[:outLen], nil
	}
	// Match libopus noise-PLC transition cadence: if periodic PLC left a pending
	// fold, consume it before switching to noise concealment.
	d.applyPendingPLCPrefilterAndFold()
	d.plcPrefilterAndFoldPending = false

	d.scratchPLCF32 = ensureFloat32Slice(&d.scratchPLCF32, outLen)
	d.concealNoisePLC(d.scratchPLCF32[:outLen], frameSize, int(prevLossDuration))
	d.finishLostFrame(framePLCNoise, frameSize)

	return d.scratchPLCF32[:outLen], nil
}

func (d *Decoder) concealNoisePLC(dst []float32, frameSize, prevLossDuration int) {
	channels := int(d.channels)
	if len(dst) < frameSize*channels {
		return
	}
	mode := GetModeConfig(frameSize)
	d.ensureBackgroundEnergyState()
	concealEnergy := ensureGLogSlice(&d.scratchPrevEnergyGLog, len(d.prevEnergy))
	copy(concealEnergy, d.prevEnergy)

	decayDB := celtGLog(0.5)
	if prevLossDuration == 0 {
		decayDB = 1.5
	}
	start := 0
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < start {
		end = start
	}
	predStride := d.predStride()
	for c := range channels {
		base := c * predStride
		for band := start; band < end; band++ {
			idx := base + band
			e := d.prevEnergy[idx] - decayDB
			if bg := d.backgroundEnergy[idx]; bg > e {
				e = bg
			}
			concealEnergy[idx] = e
		}
	}

	seed := d.rng
	if d.channels == 2 {
		d.scratchPLCHybridNormL = ensureNormSlice(&d.scratchPLCHybridNormL, frameSize)
		d.scratchPLCHybridNormR = ensureNormSlice(&d.scratchPLCHybridNormR, frameSize)
		coeffsL := d.scratchPLCHybridNormL[:frameSize]
		coeffsR := d.scratchPLCHybridNormR[:frameSize]
		clear(coeffsL)
		clear(coeffsR)
		fillHybridPLCNoiseCoeffs(coeffsL, frameSize, start, end, &seed)
		fillHybridPLCNoiseCoeffs(coeffsR, frameSize, start, end, &seed)
		if d.plcStageTrace != nil && d.plcStageTrace.armed() {
			d.plcStageTrace.capturePreSpec(0, coeffsL)
			d.plcStageTrace.capturePreSpec(1, coeffsR)
		}
		denormalizeNormCoeffsDownsample(coeffsL, concealEnergy[:predStride], end, frameSize, d.downsampleFactor())
		denormalizeNormCoeffsDownsample(coeffsR, concealEnergy[predStride:], end, frameSize, d.downsampleFactor())
		if d.plcStageTrace != nil && d.plcStageTrace.armed() {
			d.plcStageTrace.captureSpec(0, coeffsL)
			d.plcStageTrace.captureSpec(1, coeffsR)
		}
		samples := d.SynthesizeStereoFloat32(coeffsL, coeffsR, false, 1)
		n := min(len(samples), frameSize*channels)
		copy(dst[:n], samples[:n])
	} else {
		d.scratchPLCHybridNormL = ensureNormSlice(&d.scratchPLCHybridNormL, frameSize)
		coeffs := d.scratchPLCHybridNormL[:frameSize]
		clear(coeffs)
		fillHybridPLCNoiseCoeffs(coeffs, frameSize, start, end, &seed)
		if d.plcStageTrace != nil && d.plcStageTrace.armed() {
			d.plcStageTrace.capturePreSpec(0, coeffs)
		}
		denormalizeNormCoeffsDownsample(coeffs, concealEnergy[:predStride], end, frameSize, d.downsampleFactor())
		if d.plcStageTrace != nil && d.plcStageTrace.armed() {
			d.plcStageTrace.captureSpec(0, coeffs)
		}
		samples := d.SynthesizeFloat32(coeffs, false, 1)
		n := min(len(samples), frameSize*channels)
		copy(dst[:n], samples[:n])
	}
	d.setPrevEnergyGLog(concealEnergy)
	d.rng = seed

	if d.plcStageTrace != nil && d.plcStageTrace.armed() {
		d.plcStageTrace.capturePreSyn(dst[:frameSize*channels], frameSize, channels)
	}

	d.applyPostfilterFloat32(dst[:frameSize*channels], frameSize, mode.LM, int(d.postfilterPeriod), d.postfilterGain, int(d.postfilterTapset))
	if len(d.directOutPCM) >= frameSize*channels {
		d.applyDeemphasisAndScaleToFloat32(d.directOutPCM[:frameSize*channels], dst[:frameSize*channels], 1.0/32768.0)
		if d.plcStageTrace != nil && d.plcStageTrace.armed() {
			d.plcStageTrace.captureFinal(d.directOutPCM[:frameSize*channels])
		}
		return
	}
	d.applyDeemphasisAndScale(dst[:frameSize*channels], 1.0/32768.0)
	if d.plcStageTrace != nil && d.plcStageTrace.armed() {
		d.plcStageTrace.captureFinal(dst[:frameSize*channels])
	}
}

func (d *Decoder) concealPeriodicPLC(dst []float32, frameSize, lossCount int, continuePeriodic bool, commit bool) bool {
	return d.concealPeriodicPLCWithLimit(dst, frameSize, lossCount, continuePeriodic, commit, false)
}

func (d *Decoder) concealPeriodicPLCLimited(dst []float32, frameSize, lossCount int, continuePeriodic bool, commit bool) bool {
	return d.concealPeriodicPLCWithLimit(dst, frameSize, lossCount, continuePeriodic, commit, true)
}

func (d *Decoder) concealPeriodicPLCWithLimit(dst []float32, frameSize, lossCount int, continuePeriodic bool, commit bool, limitEarly bool) bool {
	if frameSize <= 0 || d.channels <= 0 {
		return false
	}
	channels := int(d.channels)
	totalSamples := frameSize + Overlap
	if len(dst) < totalSamples*channels {
		return false
	}
	if len(d.plcDecodeMem) < plcDecodeBufferSize*channels {
		return false
	}
	d.materializePLCDecodeHistory()
	if len(d.plcLPC) < celtPLCLPCOrder*channels {
		return false
	}
	// Match libopus: standalone periodic PLC is limited to the early loss
	// window, while neural/DRED PLC still computes this pitch baseline for
	// its crossfade after the regular periodic type would have stopped.
	if limitEarly && lossCount > 1 && (lossCount-1)*frameSize >= 4800 {
		return false
	}

	fade := 1.0
	period := 0
	if continuePeriodic &&
		d.plcLastPitchPeriod >= combFilterMinPeriod &&
		d.plcLastPitchPeriod <= combFilterMaxPeriod {
		period = int(d.plcLastPitchPeriod)
		fade = 0.8
	} else {
		period = d.searchPLCPitchPeriod()
	}
	if period < combFilterMinPeriod || period > combFilterMaxPeriod || period > combFilterHistory {
		return false
	}
	d.plcLastPitchPeriod = int32(period)

	const maxPeriod = combFilterMaxPeriod
	if frameSize > plcDecodeBufferSize-maxPeriod || totalSamples > plcDecodeBufferSize {
		return false
	}
	excLength := min(2*period, maxPeriod)
	if excLength <= 0 {
		return false
	}
	extrapolationOffset := maxPeriod - period
	if extrapolationOffset < 0 || extrapolationOffset+period > maxPeriod {
		return false
	}

	d.scratchPLCExc = ensureSigSlice(&d.scratchPLCExc, maxPeriod+celtPLCLPCOrder)
	d.scratchPLCFIRTmp = ensureSigSlice(&d.scratchPLCFIRTmp, excLength)
	d.scratchPLCBuf = ensureSigSlice(&d.scratchPLCBuf, plcDecodeBufferSize+Overlap)

	window := GetWindowBufferF32(Overlap)
	window32 := GetWindowBufferF32(Overlap)
	continuePeriodic = lossCount > 1 && continuePeriodic
	for ch := range channels {
		hist := d.plcDecodeMem[ch*plcDecodeBufferSize : (ch+1)*plcDecodeBufferSize]
		lpc := d.plcLPC[ch*celtPLCLPCOrder : (ch+1)*celtPLCLPCOrder]

		exc := d.scratchPLCExc[:maxPeriod+celtPLCLPCOrder]
		copy(exc, hist[plcDecodeBufferSize-maxPeriod-celtPLCLPCOrder:])

		if !continuePeriodic {
			d.computePLCLPC(exc[celtPLCLPCOrder:], lpc, window)
		}

		firStart := celtPLCLPCOrder + maxPeriod - excLength
		firTmp := d.scratchPLCFIRTmp[:excLength]
		celtFIRFloat32(firTmp, exc, firStart, excLength, lpc)
		for i := range excLength {
			v := float32(firTmp[i])
			exc[firStart+i] = celtSig(v)
		}

		decay := float32(1.0)
		decayLength := excLength >> 1
		if decayLength > 0 {
			e1 := float32(1.0)
			e2 := float32(1.0)
			base1 := celtPLCLPCOrder + maxPeriod - decayLength
			base2 := celtPLCLPCOrder + maxPeriod - 2*decayLength
			for i := range decayLength {
				v1 := float32(exc[base1+i])
				v2 := float32(exc[base2+i])
				e1 += noFMA32Mul(v1, v1)
				e2 += noFMA32Mul(v2, v2)
			}
			if e1 > e2 {
				e1 = e2
			}
			if e2 > 0 {
				decay = opusmath.SqrtF32(e1 / e2)
			}
		}

		attenuation := float32(fade) * decay
		buf := d.scratchPLCBuf[:plcDecodeBufferSize+Overlap]
		copy(buf[:plcDecodeBufferSize], hist)
		copy(buf[:plcDecodeBufferSize-frameSize], buf[frameSize:plcDecodeBufferSize])
		chOut := buf[plcDecodeBufferSize-frameSize : plcDecodeBufferSize-frameSize+totalSamples]
		s1 := float32(0)
		s1Base := plcDecodeBufferSize - maxPeriod - frameSize + extrapolationOffset
		j := 0
		for i := range totalSamples {
			if j >= period {
				j = 0
				attenuation *= decay
			}
			chOut[i] = celtSig(attenuation * float32(exc[celtPLCLPCOrder+extrapolationOffset+j]))
			srcIdx := s1Base + j
			if srcIdx >= 0 && srcIdx < len(buf) {
				v := float32(buf[srcIdx])
				s1 = noFMA32Add(s1, noFMA32Mul(v, v))
			}
			j++
		}

		d.celtIIRFloat32(chOut, hist, lpc, totalSamples)

		s2 := float32(0)
		for i := range totalSamples {
			v := float32(chOut[i])
			s2 = noFMA32Add(s2, noFMA32Mul(v, v))
		}
		if !(s1 > float32(0.2)*s2) {
			for i := range totalSamples {
				chOut[i] = 0
			}
		} else if s1 < s2 {
			ratio := opusmath.SqrtF32((s1 + 1.0) / (s2 + 1.0))
			blend := min(Overlap, totalSamples)
			for i := range blend {
				g := float32(1.0) - window32[i]*(float32(1.0)-ratio)
				chOut[i] = celtSig(float32(chOut[i]) * g)
			}
			for i := blend; i < totalSamples; i++ {
				chOut[i] = celtSig(float32(chOut[i]) * ratio)
			}
		}

		for i := range totalSamples {
			dst[i*channels+ch] = float32(chOut[i])
		}
	}

	if commit {
		d.updatePostfilterHistory(dst[:frameSize*channels], frameSize, combFilterHistory)
		d.updatePLCDecodeHistory(dst[:frameSize*channels], frameSize, plcDecodeBufferSize)
	}
	return true
}

func (d *Decoder) computePLCLPC(frame []celtSig, lpc []float32, window []float32) {
	var ac [celtPLCLPCOrder + 1]float32
	d.computePLCAutocorr(frame, window, ac[:])
	plcLPCFromAutocorr(ac[:], lpc)
}

func (d *Decoder) computePLCAutocorr(frame []celtSig, window []float32, ac []float32) {
	if len(ac) < celtPLCLPCOrder+1 {
		return
	}
	for i := 0; i <= celtPLCLPCOrder; i++ {
		ac[i] = 0
	}
	n := len(frame)
	if n <= 0 {
		return
	}
	d.scratchPLCWindowed = ensureSigSlice(&d.scratchPLCWindowed, n)
	x := d.scratchPLCWindowed[:n]
	copy(x, frame)

	overlap := Overlap
	if overlap > n>>1 {
		overlap = n >> 1
	}
	for i := 0; i < overlap && i < len(window); i++ {
		w := float32(window[i])
		x[i] = celtSig(float32(x[i]) * w)
		x[n-1-i] = celtSig(float32(x[n-1-i]) * w)
	}

	fastN := max(n-celtPLCLPCOrder, 0)
	pitchXCorrSig(x, x, ac[:celtPLCLPCOrder+1], fastN, celtPLCLPCOrder+1)
	for lag := 0; lag <= celtPLCLPCOrder; lag++ {
		tail := float32(0)
		for i := lag + fastN; i < n; i++ {
			tail += float32(x[i]) * float32(x[i-lag])
		}
		ac[lag] += tail
	}

	applyCELTAutocorrNoiseAndLagWindow32(ac[:], celtPLCLPCOrder)
}

func applyCELTAutocorrNoiseAndLagWindow32(ac []float32, order int) {
	if len(ac) <= order {
		return
	}
	ac[0] *= float32(1.0001)
	lagBase := float32(0.008) * float32(0.008)
	for i := 1; i <= order; i++ {
		lag := ac[i] * lagBase
		lag *= float32(i * i)
		ac[i] -= lag
	}
}

func plcLPCFromAutocorr(ac []float32, lpc []float32) {
	for i := range lpc {
		lpc[i] = 0
	}
	if len(ac) < len(lpc)+1 || ac[0] <= 1e-10 {
		return
	}

	var lpc32 [celtPLCLPCOrder]float32
	base := ac[0]
	errorPower := base
	for i := range lpc {
		rr := plcLPCReflectionSum(lpc32[:], ac, i)
		rr += ac[i+1]
		r := -rr / errorPower
		lpc32[i] = r
		for j := 0; j < (i+1)>>1; j++ {
			tmp1 := lpc32[j]
			tmp2 := lpc32[i-1-j]
			lpc32[j] = fma32(r, tmp2, tmp1)
			lpc32[i-1-j] = fma32(r, tmp1, tmp2)
		}
		errorPower = fma32(-(r * r), errorPower, errorPower)
		if errorPower <= float32(0.001)*base {
			break
		}
	}
	for i := range lpc {
		lpc[i] = lpc32[i]
	}
}

func plcLPCReflectionSum(lpc []float32, ac []float32, i int) float32 {
	if !libopusFloatInnerProdUsesNeonOrder {
		rr := float32(0)
		for j := range i {
			rr += lpc[j] * ac[i-j]
		}
		return rr
	}

	rr := float32(0)
	j := 0
	for ; j <= i-16; j += 16 {
		rr += mul32(lpc[j+0], ac[i-j-0])
		rr += mul32(lpc[j+1], ac[i-j-1])
		rr += mul32(lpc[j+2], ac[i-j-2])
		rr += mul32(lpc[j+3], ac[i-j-3])
		rr += mul32(lpc[j+4], ac[i-j-4])
		rr += mul32(lpc[j+5], ac[i-j-5])
		rr += mul32(lpc[j+6], ac[i-j-6])
		rr += mul32(lpc[j+7], ac[i-j-7])
		rr += mul32(lpc[j+8], ac[i-j-8])
		rr += mul32(lpc[j+9], ac[i-j-9])
		rr += mul32(lpc[j+10], ac[i-j-10])
		rr += mul32(lpc[j+11], ac[i-j-11])
		rr += mul32(lpc[j+12], ac[i-j-12])
		rr += mul32(lpc[j+13], ac[i-j-13])
		rr += mul32(lpc[j+14], ac[i-j-14])
		rr += mul32(lpc[j+15], ac[i-j-15])
	}
	for ; j <= i-4; j += 4 {
		rr += mul32(lpc[j+0], ac[i-j-0])
		rr += mul32(lpc[j+1], ac[i-j-1])
		rr += mul32(lpc[j+2], ac[i-j-2])
		rr += mul32(lpc[j+3], ac[i-j-3])
	}
	for ; j < i; j++ {
		rr = fma32(lpc[j], ac[i-j], rr)
	}
	return rr
}

func (d *Decoder) updatePLCOverlapBuffer(plcSamples []float32, frameSize int) {
	if Overlap <= 0 || frameSize <= 0 || d.channels <= 0 {
		return
	}
	channels := int(d.channels)
	totalSamples := frameSize + Overlap
	if len(plcSamples) < totalSamples*channels {
		return
	}

	overlapNeeded := Overlap * channels
	if len(d.overlapBuffer) < overlapNeeded {
		d.overlapBuffer = make([]celtSig, overlapNeeded)
	}

	if channels == 1 {
		copyFloat32ToSig(d.overlapBuffer[:Overlap], plcSamples[frameSize:frameSize+Overlap])
		return
	}

	src := frameSize * channels
	for i := range Overlap {
		d.overlapBuffer[i] = celtSig(plcSamples[src+i*channels])
		d.overlapBuffer[Overlap+i] = celtSig(plcSamples[src+i*channels+1])
	}
}

type plcPitchSearchScratch struct {
	xLP4  []float32
	yLP4  []float32
	xcorr []float32
}

func pitchXCorrFloat32(x, y, xcorr []float32, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	_ = x[length-1]
	_ = y[maxPitch+length-2]
	_ = xcorr[maxPitch-1]
	if libopusFloatPitchXCorrUsesAVX2FMA() {
		pitchXCorrFloat32AVX2FMAOrder(x, y, xcorr, length, maxPitch)
		return
	}
	if libopusFloatInnerProdUsesSSEOrder {
		pitchXCorrFloat32SSEOrder(x, y, xcorr, length, maxPitch)
		return
	}
	if pitchXcorrUsesNeonFMA {
		pitchXCorrFloat32NeonFMA(x, y, xcorr, length, maxPitch)
		return
	}
	i := 0
	for ; i < maxPitch-3; i += 4 {
		var sum [4]float32
		xcorrKernel4Float32(x, y[i:], &sum, length)
		xcorr[i] = sum[0]
		xcorr[i+1] = sum[1]
		xcorr[i+2] = sum[2]
		xcorr[i+3] = sum[3]
	}
	for ; i < maxPitch; i++ {
		xcorr[i] = innerProdFloat32(x, y[i:], length)
	}
}

// pitchXCorrFloat32NeonFMA is the fused arm64 pitch cross-correlation. The
// 4-lag blocks use the NEON FMLA kernel; the scalar tail uses celtInnerProd's
// fused arm64 path so the whole correlation runs single-rounding. Only reached
// when pitchXcorrUsesNeonFMA is set (arm64 && !purego).
func pitchXCorrFloat32NeonFMA(x, y, xcorr []float32, length, maxPitch int) {
	i := 0
	for ; i < maxPitch-3; i += 4 {
		var sum [4]float32
		xcorrKernel4Float32Neon(x, y[i:], &sum, length)
		xcorr[i] = sum[0]
		xcorr[i+1] = sum[1]
		xcorr[i+2] = sum[2]
		xcorr[i+3] = sum[3]
	}
	for ; i < maxPitch; i++ {
		xcorr[i] = innerProdFloat32(x, y[i:], length)
	}
}

func pitchXCorrSig(x, y []celtSig, xcorr []float32, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	pitchXCorrFloat32(x, y, xcorr, length, maxPitch)
}

func pitchXCorrFloat32SSEOrder(x, y, xcorr []float32, length, maxPitch int) {
	i := 0
	for ; i < maxPitch-3; i += 4 {
		var sum [4]float32
		xcorrKernel4Float32SSEOrder(x, y[i:], &sum, length)
		xcorr[i] = sum[0]
		xcorr[i+1] = sum[1]
		xcorr[i+2] = sum[2]
		xcorr[i+3] = sum[3]
	}
	for ; i < maxPitch; i++ {
		xcorr[i] = innerProdFloat32SSEOrder(x, y[i:], length)
	}
}

func xcorrKernel4Float32SSEOrder(x, y []float32, sum *[4]float32, length int) {
	// libopus celt/x86/pitch_sse.c:xcorr_kernel_sse() keeps even and odd
	// source samples in separate SIMD accumulators, then adds them lane-wise.
	var sum1 [4]float32
	var sum2 [4]float32
	for lane := range sum1 {
		sum1[lane] = sum[lane]
	}

	j := 0
	for ; j < length-3; j += 4 {
		x0 := x[j]
		sum1[0] = noFMA32Add(sum1[0], noFMA32Mul(x0, y[j]))
		sum1[1] = noFMA32Add(sum1[1], noFMA32Mul(x0, y[j+1]))
		sum1[2] = noFMA32Add(sum1[2], noFMA32Mul(x0, y[j+2]))
		sum1[3] = noFMA32Add(sum1[3], noFMA32Mul(x0, y[j+3]))

		x1 := x[j+1]
		sum2[0] = noFMA32Add(sum2[0], noFMA32Mul(x1, y[j+1]))
		sum2[1] = noFMA32Add(sum2[1], noFMA32Mul(x1, y[j+2]))
		sum2[2] = noFMA32Add(sum2[2], noFMA32Mul(x1, y[j+3]))
		sum2[3] = noFMA32Add(sum2[3], noFMA32Mul(x1, y[j+4]))

		x2 := x[j+2]
		sum1[0] = noFMA32Add(sum1[0], noFMA32Mul(x2, y[j+2]))
		sum1[1] = noFMA32Add(sum1[1], noFMA32Mul(x2, y[j+3]))
		sum1[2] = noFMA32Add(sum1[2], noFMA32Mul(x2, y[j+4]))
		sum1[3] = noFMA32Add(sum1[3], noFMA32Mul(x2, y[j+5]))

		x3 := x[j+3]
		sum2[0] = noFMA32Add(sum2[0], noFMA32Mul(x3, y[j+3]))
		sum2[1] = noFMA32Add(sum2[1], noFMA32Mul(x3, y[j+4]))
		sum2[2] = noFMA32Add(sum2[2], noFMA32Mul(x3, y[j+5]))
		sum2[3] = noFMA32Add(sum2[3], noFMA32Mul(x3, y[j+6]))
	}
	if j < length {
		xj := x[j]
		sum1[0] = noFMA32Add(sum1[0], noFMA32Mul(xj, y[j]))
		sum1[1] = noFMA32Add(sum1[1], noFMA32Mul(xj, y[j+1]))
		sum1[2] = noFMA32Add(sum1[2], noFMA32Mul(xj, y[j+2]))
		sum1[3] = noFMA32Add(sum1[3], noFMA32Mul(xj, y[j+3]))
		j++
		if j < length {
			xj = x[j]
			sum2[0] = noFMA32Add(sum2[0], noFMA32Mul(xj, y[j]))
			sum2[1] = noFMA32Add(sum2[1], noFMA32Mul(xj, y[j+1]))
			sum2[2] = noFMA32Add(sum2[2], noFMA32Mul(xj, y[j+2]))
			sum2[3] = noFMA32Add(sum2[3], noFMA32Mul(xj, y[j+3]))
			j++
			if j < length {
				xj = x[j]
				sum1[0] = noFMA32Add(sum1[0], noFMA32Mul(xj, y[j]))
				sum1[1] = noFMA32Add(sum1[1], noFMA32Mul(xj, y[j+1]))
				sum1[2] = noFMA32Add(sum1[2], noFMA32Mul(xj, y[j+2]))
				sum1[3] = noFMA32Add(sum1[3], noFMA32Mul(xj, y[j+3]))
			}
		}
	}
	for lane := range sum {
		sum[lane] = noFMA32Add(sum1[lane], sum2[lane])
	}
}

func pitchXCorrFloat32AVX2FMAOrder(x, y, xcorr []float32, length, maxPitch int) {
	i := 0
	for ; i < maxPitch-7; i += 8 {
		var sums [8]float32
		pitchXcorrKernelAVX8(x[:length], y[i:i+length+7], &sums, length)
		copy(xcorr[i:i+8], sums[:])
	}
	for ; i < maxPitch; i++ {
		xcorr[i] = innerProdFloat32SSEOrder(x, y[i:], length)
	}
}

func reduceAVX2PitchSum(sum [8]float32) float32 {
	// libopus celt/x86/pitch_avx.c:xcorr_kernel_avx() horizontally reduces
	// [0 4] [1 5] [2 6] [3 7] before the AVX hadd stages.
	s04 := noFMA32Add(sum[0], sum[4])
	s15 := noFMA32Add(sum[1], sum[5])
	s26 := noFMA32Add(sum[2], sum[6])
	s37 := noFMA32Add(sum[3], sum[7])
	return noFMA32Add(noFMA32Add(s04, s15), noFMA32Add(s26, s37))
}

func innerProdFloat32(x, y []float32, length int) float32 {
	if length <= 0 {
		return 0
	}
	_ = x[length-1]
	_ = y[length-1]
	if libopusFloatInnerProdUsesNeonOrder {
		return innerProdFloat32NeonOrder(x, y, length)
	}
	if libopusFloatInnerProdUsesSSEOrder {
		return innerProdFloat32SSEOrder(x, y, length)
	}
	sum := float32(0)
	for i := range length {
		sum += x[i] * y[i]
	}
	return sum
}

func innerProdFloat32SSEOrder(x, y []float32, length int) float32 {
	var acc [4]float32
	i := 0
	for ; i < length-3; i += 4 {
		acc[0] = noFMA32Add(acc[0], noFMA32Mul(x[i], y[i]))
		acc[1] = noFMA32Add(acc[1], noFMA32Mul(x[i+1], y[i+1]))
		acc[2] = noFMA32Add(acc[2], noFMA32Mul(x[i+2], y[i+2]))
		acc[3] = noFMA32Add(acc[3], noFMA32Mul(x[i+3], y[i+3]))
	}
	xy0 := noFMA32Add(acc[0], acc[2])
	xy1 := noFMA32Add(acc[1], acc[3])
	sum := noFMA32Add(xy0, xy1)
	for ; i < length; i++ {
		sum = noFMA32Add(sum, noFMA32Mul(x[i], y[i]))
	}
	return sum
}

func innerProdFloat32NeonOrder(x, y []float32, length int) float32 {
	var acc [4]float32
	i := 0
	for ; i < length-7; i += 8 {
		acc[0] = fma32(x[i], y[i], acc[0])
		acc[1] = fma32(x[i+1], y[i+1], acc[1])
		acc[2] = fma32(x[i+2], y[i+2], acc[2])
		acc[3] = fma32(x[i+3], y[i+3], acc[3])
		acc[0] = fma32(x[i+4], y[i+4], acc[0])
		acc[1] = fma32(x[i+5], y[i+5], acc[1])
		acc[2] = fma32(x[i+6], y[i+6], acc[2])
		acc[3] = fma32(x[i+7], y[i+7], acc[3])
	}
	if length-i >= 4 {
		acc[0] = fma32(x[i], y[i], acc[0])
		acc[1] = fma32(x[i+1], y[i+1], acc[1])
		acc[2] = fma32(x[i+2], y[i+2], acc[2])
		acc[3] = fma32(x[i+3], y[i+3], acc[3])
		i += 4
	}
	xy0 := acc[0] + acc[2]
	xy1 := acc[1] + acc[3]
	sum := xy0 + xy1
	for ; i < length; i++ {
		sum += x[i] * y[i]
	}
	return sum
}

func pitchSearchPLC(xLP []float32, y []float32, length, maxPitch int, scratch *plcPitchSearchScratch) int {
	if length <= 0 || maxPitch <= 0 {
		return 0
	}
	lag := length + maxPitch

	xLP4 := ensureFloat32Slice(&scratch.xLP4, length>>2)
	yLP4 := ensureFloat32Slice(&scratch.yLP4, lag>>2)
	xcorr := ensureFloat32Slice(&scratch.xcorr, maxPitch>>1)

	for j := 0; j < length>>2; j++ {
		xLP4[j] = xLP[2*j]
	}
	for j := 0; j < lag>>2; j++ {
		yLP4[j] = y[2*j]
	}

	pitchXCorrFloat32(xLP4, yLP4, xcorr, length>>2, maxPitch>>2)
	bestPitch := [2]int{0, 0}
	findBestPitchF32(xcorr, yLP4, length>>2, maxPitch>>2, &bestPitch)

	halfPitch := maxPitch >> 1
	ranges := pitchSearchFineRanges(bestPitch, halfPitch)
	clear(xcorr[:halfPitch])
	halfLen := length >> 1
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
		for ; i <= r.hi; i++ {
			sum := innerProdFloat32(xLP, y[i:], halfLen)
			if sum < -1 {
				sum = -1
			}
			xcorr[i] = sum
			if sum > 0 {
				xcorr16 := sum * pitchSearchXcorrScale
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

func (d *Decoder) searchPLCPitchPeriod() int {
	channels := int(d.channels)
	if channels <= 0 {
		return 0
	}
	if len(d.plcDecodeMem) < plcDecodeBufferSize*channels {
		return 0
	}
	d.materializePLCDecodeHistory()

	const (
		plcPitchLagMax = 720
		plcPitchLagMin = 100
	)
	searchLen := plcDecodeBufferSize - plcPitchLagMax
	maxPitch := plcPitchLagMax - plcPitchLagMin
	if searchLen <= 0 || maxPitch <= 0 {
		return 0
	}
	lpLen := plcDecodeBufferSize >> 1
	if lpLen <= (plcPitchLagMax >> 1) {
		return 0
	}
	d.scratchPLCPitchLP = ensureFloat32Slice(&d.scratchPLCPitchLP, lpLen)
	pitchDownsampleSig(d.plcDecodeMem, d.scratchPLCPitchLP, lpLen, channels, 2)

	searchOut := pitchSearchPLC(
		d.scratchPLCPitchLP[plcPitchLagMax>>1:],
		d.scratchPLCPitchLP,
		searchLen,
		maxPitch,
		&d.scratchPLCPitchSearch,
	)
	pitch := plcPitchLagMax - searchOut
	if pitch < combFilterMinPeriod || pitch > combFilterMaxPeriod {
		return 0
	}
	if pitch < plcPitchLagMin || pitch > plcPitchLagMax {
		return 0
	}
	return pitch
}

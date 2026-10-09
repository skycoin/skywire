package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
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

// applyPendingPLCPrefilterAndFold runs libopus prefilter_and_fold() once
// after periodic or neural concealment: it applies the inverse postfilter to
// the MDCT overlap of decode_mem and folds it as the TDAC of the next frame's
// inverse MDCT expects. It runs before the frame moves decode_mem, so the
// overlap libopus addresses as decode_mem[c]+DECODE_BUFFER_SIZE-N sits at
// DECODE_BUFFER_SIZE here.
func (d *Decoder) applyPendingPLCPrefilterAndFold() {
	if !d.plcPrefilterAndFoldPending {
		return
	}
	// Match libopus cadence: consume the pending fold exactly once.
	d.plcPrefilterAndFoldPending = false

	overlap := d.synthOverlapLen()
	if d.channels <= 0 || overlap <= 0 {
		return
	}
	d.ensureDecodeMem()
	channels := int(d.channels)
	decodeBufferSize := d.decodeMemHistoryLen()
	history := d.plcCombFilterHistoryLen()
	bufLen := history + overlap
	d.scratchPLCFoldDst = ensureSigSlice(&d.scratchPLCFoldDst, bufLen)
	window := d.scratchIMDCTF32.modeWindow(overlap)
	half := overlap >> 1

	traceArmed := d.plcStageTrace != nil && d.plcStageTrace.observeFold()

	for ch := range channels {
		mem := d.decodeMemChannel(ch)
		src := mem[decodeBufferSize-history:]
		dst := d.scratchPLCFoldDst[:bufLen]

		if traceArmed {
			d.plcStageTrace.captureCombIn(ch, src)
		}

		combFilterWithInputSig(
			dst, src, history,
			int(d.postfilterPeriodOld), int(d.postfilterPeriod), overlap,
			-d.postfilterGainOld, -d.postfilterGain,
			int(d.postfilterTapsetOld), int(d.postfilterTapset),
			nil, 0,
		)

		etmp := dst[history : history+overlap]
		if traceArmed {
			d.plcStageTrace.captureCombOut(ch, etmp)
		}
		fold := mem[decodeBufferSize : decodeBufferSize+half]
		for i := range fold {
			// Simulate TDAC blending exactly where libopus mutates decode_mem.
			w0 := float32(window[i])
			w1 := float32(window[overlap-1-i])
			x0 := float32(etmp[overlap-1-i])
			x1 := float32(etmp[i])
			// prefilter_and_fold: w[i]*etmp[ov-1-i] + w[ov-1-i]*etmp[i]. clang
			// fuses the left product and gcc fuses neither.
			fold[i] = celtSig(fma32(w0, x0, noFMA32Mul(w1, x1)))
		}
		if traceArmed {
			d.plcStageTrace.captureFold(ch, mem[decodeBufferSize:])
		}
	}
}

func (d *Decoder) accumulatePLCLossDuration(frameSize int) {
	lm := min(max(d.modeConfig(frameSize).LM, 0), 30)
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
	if end > d.predStride() {
		end = d.predStride()
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

// DecodeHybridFECPLC conceals a lost Hybrid-mode CELT frame of frameSize
// samples at the active CELT mode rate and accumulates the concealed highband onto out, which
// holds the SILK lowband: libopus celt_decode_with_ec(NULL) with celt_accum=1
// (opus_decode_frame for a lost or FEC-recovered Hybrid frame). out is either
// frameSize*channels long or sized for the API rate, in which case the
// de-emphasis downsamples into it.
func (d *Decoder) DecodeHybridFECPLC(frameSize int, out []float32) error {
	if !d.validHybridFrameSize(frameSize) && frameSize != d.synthOverlapLen() && frameSize != d.synthOverlapLen()*2 {
		return ErrInvalidFrameSize
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
	decayDB := celtGLog(0.5)
	if prevLossDuration == 0 {
		decayDB = 1.5
	}
	d.ensureBackgroundEnergyState()
	concealEnergy := ensureGLogSlice(&d.scratchPrevEnergyGLog, len(d.prevEnergy))
	copy(concealEnergy, d.prevEnergy)

	// Match libopus celt_decode_lost() noise PLC cadence: in hybrid mode,
	// only the coded CELT band range [start,end) gets decayed/floored.
	mode := d.modeConfig(frameSize)
	start := HybridCELTStartBand
	end := min(EffectiveBandsForFrameSize(d.bandwidth, frameSize), mode.EffBands)
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
		fillHybridPLCNoiseCoeffs(coeffsL, start, end, mode.LM, d.modeEdges(), &seed)
		fillHybridPLCNoiseCoeffs(coeffsR, start, end, mode.LM, d.modeEdges(), &seed)
		denormalizeHybridPLCNoiseCoeffs(coeffsL, concealEnergy[:predStride], start, end, mode.LM, d.modeEdges(), d.downsampleFactor())
		denormalizeHybridPLCNoiseCoeffs(coeffsR, concealEnergy[predStride:], start, end, mode.LM, d.modeEdges(), d.downsampleFactor())
		d.synthesizeToDecodeMem(coeffsL, coeffsR, frameSize, 1, false)
	} else {
		d.scratchPLCHybridNormL = ensureNormSlice(&d.scratchPLCHybridNormL, frameSize)
		coeffs := d.scratchPLCHybridNormL[:frameSize]
		clear(coeffs)
		fillHybridPLCNoiseCoeffs(coeffs, start, end, mode.LM, d.modeEdges(), &seed)
		denormalizeHybridPLCNoiseCoeffs(coeffs, concealEnergy[:predStride], start, end, mode.LM, d.modeEdges(), d.downsampleFactor())
		d.synthesizeToDecodeMem(coeffs, nil, frameSize, 1, false)
	}
	d.setPrevEnergyGLog(concealEnergy)
	d.rng = seed

	d.postfilterDecodeMem(frameSize, mode.LM, int(d.postfilterPeriod), d.postfilterGain, int(d.postfilterTapset))
	savedOut, savedAccum := d.directOutPCM, d.directOutAccum
	d.directOutPCM, d.directOutAccum = out, true
	d.deemphasisDecodeMem(frameSize)
	d.directOutPCM, d.directOutAccum = savedOut, savedAccum
	return nil
}

func fillHybridPLCNoiseCoeffs(coeffs []celtNorm, startBand, endBand, lm int, edges []int, seed *uint32) {
	if len(coeffs) == 0 || lm < 0 || len(edges) < endBand+1 {
		return
	}
	bandScale := 1 << uint(lm)
	if startBand < 0 {
		startBand = 0
	}
	if endBand >= len(edges) {
		endBand = len(edges) - 1
	}
	if endBand < startBand {
		endBand = startBand
	}

	for band := startBand; band < endBand; band++ {
		start := edges[band] * bandScale
		end := edges[band+1] * bandScale
		if start < 0 {
			start = 0
		}
		if end > len(coeffs) {
			end = len(coeffs)
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

// denormalizeHybridPLCNoiseCoeffs applies the active mode's per-band energy
// gains to noise-PLC vectors. libopus celt_decode_lost() builds each band's
// bins at eBands[i]<<LM and then passes that vector through celt_synthesis();
// using frameSize/Overlap is only equivalent for the 48 kHz modes.
func denormalizeHybridPLCNoiseCoeffs(coeffs []celtNorm, energies []celtGLog, startBand, endBand, lm int, edges []int, downsample int) {
	if len(coeffs) == 0 || len(energies) == 0 || lm < 0 || endBand <= startBand || len(edges) < endBand+1 {
		return
	}
	if startBand < 0 {
		startBand = 0
	}
	if endBand > len(energies) {
		endBand = len(energies)
	}
	if endBand <= startBand || len(edges) < endBand+1 {
		return
	}
	bandScale := 1 << uint(lm)
	bound := min(edges[endBand]*bandScale, len(coeffs))
	if downsample > 1 {
		bound = min(bound, len(coeffs)/downsample)
	}
	for band := startBand; band < endBand; band++ {
		from := min(edges[band]*bandScale, bound)
		to := min(edges[band+1]*bandScale, bound)
		if from >= to {
			continue
		}
		gain := denormalizeBandGain(energies, band)
		for i := from; i < to; i++ {
			coeffs[i] = celtNorm(float32(coeffs[i]) * gain)
		}
	}
	clear(coeffs[bound:])
}

// decodePLC generates concealment audio for a lost CELT packet.
func (d *Decoder) decodePLC(frameSize int) ([]float32, error) {
	if !d.validFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Keep PLC loss cadence bookkeeping.
	prevLossDuration := d.plcLossDuration
	_ = d.plcState.RecordLoss()
	lossCount := d.plcState.LostCount()

	// Ensure scratch buffer is large enough
	channels := int(d.channels)
	outLen := frameSize * channels
	plcLen := (frameSize + d.synthOverlapLen()) * channels
	d.scratchPLC = ensureFloat32Slice(&d.scratchPLC, plcLen)

	currFrameType := d.chooseLostFrameType(0, false, false)

	// Match libopus decode_lost() mode cadence: favor periodic concealment in the
	// early loss window and fall back to noise-based concealment when unavailable.
	if currFrameType == framePLCPeriodic &&
		d.concealPeriodicPLCLimited(d.scratchPLC[:plcLen], frameSize, lossCount, d.lastPLCFrameWasPeriodic(), true) {
		d.finishLostFrame(framePLCPeriodic, frameSize)
		d.plcPrefilterAndFoldPending = true
		return d.deemphasisInterleaved(d.scratchPLC[:outLen], frameSize), nil
	}
	// Match libopus noise-PLC transition cadence: if periodic PLC left a pending
	// fold, consume it before switching to noise concealment.
	d.applyPendingPLCPrefilterAndFold()
	d.plcPrefilterAndFoldPending = false

	samples := d.concealNoisePLC(frameSize, int(prevLossDuration))
	d.finishLostFrame(framePLCNoise, frameSize)

	return samples, nil
}

// concealNoisePLC is the noise-based branch of libopus celt_decode_lost():
// decayed band energies drive renormalised noise through celt_synthesis, and
// the postfilter runs with the last parameters.
func (d *Decoder) concealNoisePLC(frameSize, prevLossDuration int) []float32 {
	channels := int(d.channels)
	mode := d.modeConfig(frameSize)
	d.ensureBackgroundEnergyState()
	concealEnergy := ensureGLogSlice(&d.scratchPrevEnergyGLog, len(d.prevEnergy))
	copy(concealEnergy, d.prevEnergy)

	decayDB := celtGLog(0.5)
	if prevLossDuration == 0 {
		decayDB = 1.5
	}
	start := 0
	end := d.effectiveEndBand(frameSize)
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
		d.fillPLCNoiseCoeffs(coeffsL, frameSize, start, end, &seed)
		d.fillPLCNoiseCoeffs(coeffsR, frameSize, start, end, &seed)
		if d.plcStageTrace != nil && d.plcStageTrace.armed() {
			d.plcStageTrace.capturePreSpec(0, coeffsL)
			d.plcStageTrace.capturePreSpec(1, coeffsR)
		}
		denormalizeBandsPackedDownsampleIntoFloat32(coeffsL, coeffsL, concealEnergy[:predStride], start, end, mode.LM, d.modeEdges(), d.downsampleFactor())
		denormalizeBandsPackedDownsampleIntoFloat32(coeffsR, coeffsR, concealEnergy[predStride:], start, end, mode.LM, d.modeEdges(), d.downsampleFactor())
		if d.plcStageTrace != nil && d.plcStageTrace.armed() {
			d.plcStageTrace.captureSpec(0, coeffsL)
			d.plcStageTrace.captureSpec(1, coeffsR)
		}
		d.synthesizeToDecodeMem(coeffsL, coeffsR, frameSize, 1, false)
	} else {
		d.scratchPLCHybridNormL = ensureNormSlice(&d.scratchPLCHybridNormL, frameSize)
		coeffs := d.scratchPLCHybridNormL[:frameSize]
		clear(coeffs)
		d.fillPLCNoiseCoeffs(coeffs, frameSize, start, end, &seed)
		if d.plcStageTrace != nil && d.plcStageTrace.armed() {
			d.plcStageTrace.capturePreSpec(0, coeffs)
		}
		denormalizeBandsPackedDownsampleIntoFloat32(coeffs, coeffs, concealEnergy[:predStride], start, end, mode.LM, d.modeEdges(), d.downsampleFactor())
		if d.plcStageTrace != nil && d.plcStageTrace.armed() {
			d.plcStageTrace.captureSpec(0, coeffs)
		}
		d.synthesizeToDecodeMem(coeffs, nil, frameSize, 1, false)
	}
	d.setPrevEnergyGLog(concealEnergy)
	d.rng = seed

	if d.plcStageTrace != nil && d.plcStageTrace.armed() {
		for c := range channels {
			d.plcStageTrace.capturePreSyn(c, d.outSyn(c, frameSize)[:frameSize])
		}
	}

	d.postfilterDecodeMem(frameSize, mode.LM, int(d.postfilterPeriod), d.postfilterGain, int(d.postfilterTapset))
	samples := d.deemphasisDecodeMem(frameSize)
	if d.plcStageTrace != nil && d.plcStageTrace.armed() {
		if samples != nil {
			d.plcStageTrace.captureFinal(samples)
		} else {
			d.plcStageTrace.captureFinal(d.directOutPCM[:min(len(d.directOutPCM), frameSize*channels)])
		}
	}
	return samples
}

func (d *Decoder) concealPeriodicPLC(dst []float32, frameSize, lossCount int, continuePeriodic bool, commit bool) bool {
	return d.concealPeriodicPLCWithLimit(dst, frameSize, lossCount, continuePeriodic, commit, false)
}

func (d *Decoder) concealPeriodicPLCLimited(dst []float32, frameSize, lossCount int, continuePeriodic bool, commit bool) bool {
	return d.concealPeriodicPLCWithLimit(dst, frameSize, lossCount, continuePeriodic, commit, true)
}

// periodicPLCEnergy follows the selected non-native-96 PLC energy reduction.
// On arm64 that path accumulates complete four-sample groups separately and
// contracts the short remainder; celt_decode_lost's 48 kHz geometry uses it.
func periodicPLCEnergy(sum float32, samples []celtSig) float32 {
	if libopusFloatInnerProdUsesNeonOrder {
		vectorEnd := len(samples) &^ 3
		for i := 0; i < vectorEnd; i++ {
			sample := float32(samples[i])
			sum = noFMA32Add(sum, noFMA32Mul(sample, sample))
		}
		for i := vectorEnd; i < len(samples); i++ {
			sample := float32(samples[i])
			sum = fma32(sample, sample, sum)
		}
		return sum
	}
	if libopusFloatInnerProdUsesSSEOrder {
		for _, value := range samples {
			sample := float32(value)
			sum = noFMA32Add(sum, noFMA32Mul(sample, sample))
		}
		return sum
	}
	for _, value := range samples {
		sample := float32(value)
		sum = fma32(sample, sample, sum)
	}
	return sum
}

// periodicPLCDecayEnergy matches celt_decode_lost's initial E1/E2 loop. The
// ENABLE_QEXT arm64 build emits source-order scalar FMAs for these sums; the
// default build follows periodicPLCEnergy's selected reduction.
func periodicPLCDecayEnergy(sum float32, samples []celtSig) float32 {
	if extsupport.QEXT && libopusFloatInnerProdUsesNeonOrder {
		for _, value := range samples {
			sample := float32(value)
			sum = fma32(sample, sample, sum)
		}
		return sum
	}
	return periodicPLCEnergy(sum, samples)
}

func (d *Decoder) plcSynthesisEnergy(sum float32, samples []celtSig) float32 {
	if d.qextDecodeScale() == 2 {
		// celt_decode_lost's selected ARM SIMD build rounds each S2 product
		// before adding it, while its scalar build uses a fused multiply-add.
		if libopusFloatInnerProdUsesNeonOrder {
			for _, value := range samples {
				sample := float32(value)
				sum = noFMA32Add(sum, noFMA32Mul(sample, sample))
			}
		} else {
			for _, value := range samples {
				sample := float32(value)
				sum = fma32(sample, sample, sum)
			}
		}
		return sum
	}
	return periodicPLCEnergy(sum, samples)
}

func (d *Decoder) concealPeriodicPLCWithLimit(dst []float32, frameSize, lossCount int, continuePeriodic bool, commit bool, limitEarly bool) bool {
	overlap := d.synthOverlapLen()
	if frameSize <= 0 || d.channels <= 0 {
		return false
	}
	channels := int(d.channels)
	totalSamples := frameSize + overlap
	decodeBufferSize := d.plcDecodeBufferLen()
	maxPeriod := d.plcCombFilterMaxPeriod()
	if len(dst) < totalSamples*channels {
		return false
	}
	d.ensureDecodeMem()
	if len(d.plcLPC) < celtPLCLPCOrder*channels {
		return false
	}
	// Match libopus: standalone periodic PLC is limited to the early loss
	// window, while neural/DRED PLC still computes this pitch baseline for
	// its crossfade after the regular periodic type would have stopped.
	// celt_decode_lost() measures plc_duration in units of 1 << LM.
	if limitEarly && d.plcDuration >= 40 {
		return false
	}

	fade := 1.0
	period := 0
	if continuePeriodic &&
		d.plcLastPitchPeriod >= int32(d.plcCombFilterMinPeriod()) &&
		d.plcLastPitchPeriod <= int32(d.plcCombFilterMaxPeriod()) {
		period = int(d.plcLastPitchPeriod)
		fade = 0.8
	} else {
		period = d.searchPLCPitchPeriod()
	}
	if period < d.plcCombFilterMinPeriod() || period > maxPeriod {
		return false
	}
	d.plcLastPitchPeriod = int32(period)

	if frameSize > decodeBufferSize-maxPeriod || totalSamples > decodeBufferSize {
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
	// Pitch changes across losses; reserve the full excitation bound once.
	d.scratchPLCFIRTmp = ensureSigSlice(&d.scratchPLCFIRTmp, maxPeriod)
	d.scratchPLCBuf = ensureSigSlice(&d.scratchPLCBuf, decodeBufferSize+overlap)

	window := d.scratchIMDCTF32.modeWindow(overlap)
	window32 := d.scratchIMDCTF32.modeWindow(overlap)
	continuePeriodic = lossCount > 1 && continuePeriodic
	for ch := range channels {
		hist := d.decodeMemChannel(ch)[:decodeBufferSize]
		lpc := d.plcLPC[ch*celtPLCLPCOrder : (ch+1)*celtPLCLPCOrder]

		exc := d.scratchPLCExc[:maxPeriod+celtPLCLPCOrder]
		copy(exc, hist[decodeBufferSize-maxPeriod-celtPLCLPCOrder:])

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
			e1 = periodicPLCDecayEnergy(e1, exc[base1:base1+decayLength])
			e2 = periodicPLCDecayEnergy(e2, exc[base2:base2+decayLength])
			if e1 > e2 {
				e1 = e2
			}
			if e2 > 0 {
				decay = opusmath.SqrtF32(e1 / e2)
			}
		}

		attenuation := float32(fade) * decay
		buf := d.scratchPLCBuf[:decodeBufferSize+overlap]
		copy(buf[:decodeBufferSize], hist)
		copy(buf[:decodeBufferSize-frameSize], buf[frameSize:decodeBufferSize])
		chOut := buf[decodeBufferSize-frameSize : decodeBufferSize-frameSize+totalSamples]
		s1 := float32(0)
		s1Base := decodeBufferSize - maxPeriod - frameSize + extrapolationOffset
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
				// celt_decoder.c celt_decode_lost accumulates S1 scalar-wise;
				// arm64 contracts the product and sum, while amd64 does not.
				s1 = fma32(v, v, s1)
			}
			j++
		}

		d.celtIIRFloat32(chOut, hist, lpc, totalSamples)

		// celt_decoder.c celt_decode_lost accumulates S2 with the target's
		// selected reduction order: arm64 NEON rounds four products before
		// adding and contracts the tail; scalar arm64 contracts every sample.
		s2 := d.plcSynthesisEnergy(0, chOut[:totalSamples])
		if !(s1 > float32(0.2)*s2) {
			for i := range totalSamples {
				chOut[i] = 0
			}
		} else if s1 < s2 {
			ratio := opusmath.SqrtF32((s1 + 1.0) / (s2 + 1.0))
			blend := min(overlap, totalSamples)
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
		d.commitInterleavedToDecodeMem(dst[:totalSamples*channels], frameSize)
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
	d.computePLCRawAutocorr(frame, window, ac)
	applyCELTPLCLagWindow32(ac[:celtPLCLPCOrder+1], celtPLCLPCOrder)
}

func (d *Decoder) computePLCRawAutocorr(frame []celtSig, window []float32, ac []float32) {
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

	overlap := min(d.synthOverlapLen(), n>>1)
	for i := 0; i < overlap && i < len(window); i++ {
		w := float32(window[i])
		x[i] = celtSig(float32(x[i]) * w)
		x[n-1-i] = celtSig(float32(x[n-1-i]) * w)
	}

	fastN := max(n-celtPLCLPCOrder, 0)
	pitchXCorrSig(x, x, ac[:celtPLCLPCOrder+1], fastN, celtPLCLPCOrder+1)
	for lag := 0; lag <= celtPLCLPCOrder; lag++ {
		tail := float32(0)
		if pitchXcorrUsesNeonFMA {
			// The selected arm64 SIMD celt_lpc.o rounds products in complete
			// four-term groups (batched by 16 for longer tails), adds them in
			// order, then uses FMAs for residual samples.
			tail = celtPLCAutocorrTailNeon(x, lag+fastN, lag, n)
		} else {
			for i := lag + fastN; i < n; i++ {
				tail += float32(x[i]) * float32(x[i-lag])
			}
		}
		if pitchXcorrUsesNeonFMA {
			ac[lag] = noFMA32Add(ac[lag], tail)
		} else {
			ac[lag] += tail
		}
	}
}

func celtPLCAutocorrTailNeon(x []celtSig, start, lag, end int) float32 {
	tail := float32(0)
	i := start
	for ; i+4 <= end; i += 4 {
		var products [4]float32
		for j := range products {
			products[j] = noFMA32Mul(float32(x[i+j]), float32(x[i+j-lag]))
		}
		for _, product := range products {
			tail = noFMA32Add(tail, product)
		}
	}
	for ; i < end; i++ {
		tail = fma32(float32(x[i]), float32(x[i-lag]), tail)
	}
	return tail
}

func applyCELTPitchLagWindow32(ac []float32, order int) {
	if len(ac) <= order {
		return
	}
	ac[0] = float32(ac[0] * float32(1.0001))
	const lagCoefficient = float32(0.008)
	for i := 1; i <= order; i++ {
		lag := float32(lagCoefficient * float32(i))
		damped := float32(ac[i] * lag)
		// pitch.c forms the second product with the subtraction, allowing
		// the target's normal FP contraction after the rounded first product.
		ac[i] = fma32(-lag, damped, ac[i])
	}
}

func applyCELTPLCLagWindow32(ac []float32, order int) {
	if len(ac) <= order {
		return
	}
	ac[0] = float32(ac[0] * float32(1.0001))
	const lagCoefficient = float32(0.008)
	lagBase := float32(lagCoefficient * lagCoefficient)
	for i := 1; i <= order; i++ {
		damped := float32(ac[i] * lagBase)
		damped = float32(damped * float32(i))
		// celt_decoder.c leaves the final `* i` in `ac[i] -= ...`, so C
		// contracts that product with the subtraction where supported.
		ac[i] = fma32(-damped, float32(i), ac[i])
	}
}

func plcLPCFromAutocorr(ac []float32, lpc []float32) {
	for i := range lpc {
		lpc[i] = 0
	}
	if len(ac) < len(lpc)+1 || !(ac[0] > 1e-10) {
		return
	}

	var lpc32 [celtPLCLPCOrder]float32
	base := ac[0]
	errorPower := base
	for i := range lpc {
		rr := plcLPCReflectionSumOrdered(lpc32[:], ac, i)
		rr += ac[i+1]
		r := -rr / errorPower
		lpc32[i] = r
		for j := 0; j < (i+1)>>1; j++ {
			tmp1 := lpc32[j]
			tmp2 := lpc32[i-1-j]
			lpc32[j] = fma32(r, tmp2, tmp1)
			lpc32[i-1-j] = fma32(r, tmp1, tmp2)
		}
		errorPower = plcLPCErrorPowerUpdate32(r, errorPower)
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
	for ; i < maxPitch-7; i += 8 {
		var sum [8]float32
		xcorrKernel8Float32(x, y[i:], &sum, length)
		xcorr[i] = sum[0]
		xcorr[i+1] = sum[1]
		xcorr[i+2] = sum[2]
		xcorr[i+3] = sum[3]
		xcorr[i+4] = sum[4]
		xcorr[i+5] = sum[5]
		xcorr[i+6] = sum[6]
		xcorr[i+7] = sum[7]
	}
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

// pitchXCorrFloat32Quality is the encoder pitch cross-correlation. The scalar
// path uses libopus pitch.c's ordered four-lag xcorr_kernel_c accumulation so
// near-tied pitch candidates make the same discrete choice.
func pitchXCorrFloat32Quality(x, y, xcorr []float32, length, maxPitch int) {
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
// 4-lag blocks use libopus' ordered NEON FMLA kernel; the scalar tail uses
// celtInnerProd's fused arm64 path so the whole correlation runs
// single-rounding. Only reached when pitchXcorrUsesNeonFMA is set
// (arm64 && !nosimd).
func pitchXCorrFloat32NeonFMA(x, y, xcorr []float32, length, maxPitch int) {
	i := 0
	for ; i < maxPitch-3; i += 4 {
		var sum [4]float32
		xcorrKernel4Float32NeonOrdered(x, y[i:], &sum, length)
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
	pitchXCorrFloat32PLC(x, y, xcorr, length, maxPitch)
}

// pitchXCorrFloat32PLC is the loss-concealment pitch cross-correlation. It is
// pitchXCorrFloat32 without the encoder NEON branch: the arm64 decode path uses
// a scalar-order kernel whose contracted FMAs match the single-chain NEON
// accumulation in libopus. The amd64 SSE/AVX2 branches match libopus's x86
// PLC kernels. Encoder pitch search
// uses its selected-C matched correlation path.
func pitchXCorrFloat32PLC(x, y, xcorr []float32, length, maxPitch int) {
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
	if length <= 0 {
		return
	}
	// libopus celt/x86/pitch_sse.c:xcorr_kernel_sse() keeps even and odd
	// source samples in separate SIMD accumulators, then adds them lane-wise.
	// The kernel reads x[0:length] and y[0:length+3]; slicing to those bounds
	// and advancing the slices (prove cannot reason about stride-4 counters)
	// removes every per-access bounds check, and scalar accumulators keep the
	// 8 lanes in FP registers. The multiply/add sequence is unchanged.
	x = x[:length]
	y = y[:length+3]
	s10, s11, s12, s13 := sum[0], sum[1], sum[2], sum[3]
	var s20, s21, s22, s23 float32

	for len(x) >= 4 && len(y) >= 7 {
		x0 := x[0]
		s10 = noFMA32Add(s10, noFMA32Mul(x0, y[0]))
		s11 = noFMA32Add(s11, noFMA32Mul(x0, y[1]))
		s12 = noFMA32Add(s12, noFMA32Mul(x0, y[2]))
		s13 = noFMA32Add(s13, noFMA32Mul(x0, y[3]))

		x1 := x[1]
		s20 = noFMA32Add(s20, noFMA32Mul(x1, y[1]))
		s21 = noFMA32Add(s21, noFMA32Mul(x1, y[2]))
		s22 = noFMA32Add(s22, noFMA32Mul(x1, y[3]))
		s23 = noFMA32Add(s23, noFMA32Mul(x1, y[4]))

		x2 := x[2]
		s10 = noFMA32Add(s10, noFMA32Mul(x2, y[2]))
		s11 = noFMA32Add(s11, noFMA32Mul(x2, y[3]))
		s12 = noFMA32Add(s12, noFMA32Mul(x2, y[4]))
		s13 = noFMA32Add(s13, noFMA32Mul(x2, y[5]))

		x3 := x[3]
		s20 = noFMA32Add(s20, noFMA32Mul(x3, y[3]))
		s21 = noFMA32Add(s21, noFMA32Mul(x3, y[4]))
		s22 = noFMA32Add(s22, noFMA32Mul(x3, y[5]))
		s23 = noFMA32Add(s23, noFMA32Mul(x3, y[6]))

		x = x[4:]
		y = y[4:]
	}
	if len(x) >= 1 && len(y) >= 4 {
		xj := x[0]
		s10 = noFMA32Add(s10, noFMA32Mul(xj, y[0]))
		s11 = noFMA32Add(s11, noFMA32Mul(xj, y[1]))
		s12 = noFMA32Add(s12, noFMA32Mul(xj, y[2]))
		s13 = noFMA32Add(s13, noFMA32Mul(xj, y[3]))
		x = x[1:]
		y = y[1:]
		if len(x) >= 1 && len(y) >= 4 {
			xj = x[0]
			s20 = noFMA32Add(s20, noFMA32Mul(xj, y[0]))
			s21 = noFMA32Add(s21, noFMA32Mul(xj, y[1]))
			s22 = noFMA32Add(s22, noFMA32Mul(xj, y[2]))
			s23 = noFMA32Add(s23, noFMA32Mul(xj, y[3]))
			x = x[1:]
			y = y[1:]
			if len(x) >= 1 && len(y) >= 4 {
				xj = x[0]
				s10 = noFMA32Add(s10, noFMA32Mul(xj, y[0]))
				s11 = noFMA32Add(s11, noFMA32Mul(xj, y[1]))
				s12 = noFMA32Add(s12, noFMA32Mul(xj, y[2]))
				s13 = noFMA32Add(s13, noFMA32Mul(xj, y[3]))
			}
		}
	}
	sum[0] = noFMA32Add(s10, s20)
	sum[1] = noFMA32Add(s11, s21)
	sum[2] = noFMA32Add(s12, s22)
	sum[3] = noFMA32Add(s13, s23)
}

func pitchXCorrFloat32AVX2FMAOrder(x, y, xcorr []float32, length, maxPitch int) {
	if length < 16 {
		pitchXCorrFloat32AVX2FMAOrderTiny(x, y, xcorr, length, maxPitch)
		return
	}
	i := pitchXCorrAVX2Blocks(x, y, xcorr, length, maxPitch)
	for ; i < maxPitch-7; i += 8 {
		var sums [8]float32
		pitchXcorrKernelAVX8(x[:length], y[i:i+length+7], &sums, length)
		copy(xcorr[i:i+8], sums[:])
	}
	if i < maxPitch {
		innerProdFloat32SSEOrderLags(x, y[i:], xcorr[i:maxPitch], length)
	}
}

func pitchXCorrFloat32AVX2FMAOrderTinyScalar(x, y, xcorr []float32, length, maxPitch int) {
	if maxPitch <= 0 {
		return
	}
	avxLimit := maxPitch &^ 7
	for pitch := 0; pitch < avxLimit; pitch++ {
		var lanes [8]float32
		for j := 0; j < length; j++ {
			xv, yv := x[j], y[pitch+j]
			lane := j & 7
			if j < 8 {
				product := xv * yv
				if xv != 0 && yv != 0 && product == product {
					// FMA(x, y, +0) rounds the product once to float32.
					lanes[lane] = product
				} else {
					// Keep signed-zero and NaN behavior of the initial fused step.
					lanes[lane] = opusmath.FMA32(xv, yv, 0)
				}
			} else {
				lanes[lane] = opusmath.FMA32(xv, yv, lanes[lane])
			}
		}
		xcorr[pitch] = reduceAVX2PitchSum(lanes)
	}
	for pitch := avxLimit; pitch < maxPitch; pitch++ {
		xcorr[pitch] = innerProdFloat32SSEOrder(x, y[pitch:], length)
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
		return celtInnerProd8FMA32(x[:length], y[:length], length)
	}
	if libopusFloatInnerProdUsesSSEOrder {
		return innerProdFloat32SSEOrder(x, y, length)
	}
	x = x[:length]
	y = y[:length]
	// libopus celt_inner_prod_c is one serial MAC16_16 chain. Keep the
	// scalar path in source order; the split accumulators belong to the
	// explicitly paired NEON and SSE variants above.
	var sum float32
	for i := range x {
		sum = fma32(x[i], y[i], sum)
	}
	return sum
}

func innerProdFloat32SSEOrderScalar(x, y []float32, length int) float32 {
	if length <= 0 {
		return 0
	}
	// Slicing to length, advancing the slices, and using scalar accumulators
	// keeps the four SSE lanes in FP registers with no bounds checks. The vector
	// body and horizontal reduction retain SSE order; the remainder follows the
	// target's scalar MAC16_16 operation.
	x = x[:length]
	y = y[:length]
	var acc0, acc1, acc2, acc3 float32
	for len(x) >= 4 && len(y) >= 4 {
		acc0 = noFMA32Add(acc0, noFMA32Mul(x[0], y[0]))
		acc1 = noFMA32Add(acc1, noFMA32Mul(x[1], y[1]))
		acc2 = noFMA32Add(acc2, noFMA32Mul(x[2], y[2]))
		acc3 = noFMA32Add(acc3, noFMA32Mul(x[3], y[3]))
		x = x[4:]
		y = y[4:]
	}
	xy0 := noFMA32Add(acc0, acc2)
	xy1 := noFMA32Add(acc1, acc3)
	sum := noFMA32Add(xy0, xy1)
	for i := 0; i < len(x) && i < len(y); i++ {
		sum = pitchXcorrSSETailMAC32(sum, x[i], y[i])
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

	pitchXCorrFloat32PLC(xLP4, yLP4, xcorr, length>>2, maxPitch>>2)
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
	d.ensureDecodeMem()
	decodeBufferSize := d.decodeMemHistoryLen()

	const (
		plcPitchLagMax = 720
		plcPitchLagMin = 100
	)
	searchLen := plcDecodeBufferSize - plcPitchLagMax
	maxPitch := plcPitchLagMax - plcPitchLagMin
	if searchLen <= 0 || maxPitch <= 0 {
		return 0
	}
	// celt_plc_pitch_search() keeps its 48 kHz analysis geometry under QEXT,
	// while pitch_downsample() reads QEXT_SCALE(2) samples per output and the
	// resulting period is multiplied by QEXT_SCALE before synthesis.
	lpLen := plcDecodeBufferSize >> 1
	if lpLen <= (plcPitchLagMax >> 1) {
		return 0
	}
	d.scratchPLCPitchLP = ensureFloat32Slice(&d.scratchPLCPitchLP, lpLen)
	qextScale := d.qextDecodeScale()
	// pitch_downsample() reads both channels' decode history; it takes them
	// as one buffer split in half.
	hist := d.decodeMemChannel(0)[:decodeBufferSize]
	if channels == 2 {
		hist = ensureSigSliceNoClear(&d.scratchPLCPitchHist, 2*decodeBufferSize)
		copy(hist, d.decodeMemChannel(0)[:decodeBufferSize])
		copy(hist[decodeBufferSize:], d.decodeMemChannel(1)[:decodeBufferSize])
	}
	pitchDownsampleSig(hist, d.scratchPLCPitchLP, lpLen, channels, 2*qextScale)

	searchOut := pitchSearchPLC(
		d.scratchPLCPitchLP[plcPitchLagMax>>1:],
		d.scratchPLCPitchLP,
		searchLen,
		maxPitch,
		&d.scratchPLCPitchSearch,
	)
	pitch := (plcPitchLagMax - searchOut) * qextScale
	if pitch < d.plcCombFilterMinPeriod() || pitch > d.plcCombFilterMaxPeriod() {
		return 0
	}
	if pitch < plcPitchLagMin*qextScale || pitch > plcPitchLagMax*qextScale {
		return 0
	}
	return pitch
}

func (d *Decoder) fillPLCNoiseCoeffs(coeffs []celtNorm, frameSize, startBand, endBand int, seed *uint32) {
	if len(coeffs) < frameSize || frameSize <= 0 {
		return
	}
	if startBand < 0 {
		startBand = 0
	}
	if endBand > d.modeNbEBands() {
		endBand = d.modeNbEBands()
	}
	if endBand < startBand {
		endBand = startBand
	}

	edges := d.modeEdges()
	lm := d.modeConfig(frameSize).LM
	for band := startBand; band < endBand; band++ {
		start := edges[band] << lm
		end := edges[band+1] << lm
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

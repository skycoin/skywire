package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// NormalizeBandsToArrayMonoWithBandE normalizes MDCT coefficients for mono
// and returns the normalized coefficients and linear band amplitudes.
func (e *Encoder) NormalizeBandsToArrayMonoWithBandE(mdctCoeffs []float32, nbBands, frameSize int) (norm []celtNorm, bandE []celtEner) {
	norm = ensureNormSlice(&e.scratch.normL, frameSize)
	bandE = ensureEnerSlice(&e.scratch.bandE, nbBands)
	NormalizeBandsToArrayInto(mdctCoeffs, nbBands, frameSize, norm, bandE)
	if e.lfe {
		applyLFELinearBandEClamp(bandE, nbBands, 1)
		normalizeBandsWithBandEInto(mdctCoeffs, nbBands, frameSize, norm, bandE)
	}
	return norm, bandE
}

// NormalizeBandsToArrayMonoWithBandEF32 normalizes float-build MDCT
// coefficients for mono and returns normalized coefficients plus amplitudes.
func (e *Encoder) NormalizeBandsToArrayMonoWithBandEF32(mdctCoeffs []float32, nbBands, frameSize int) (norm []celtNorm, bandE []celtEner) {
	norm = ensureNormSlice(&e.scratch.normL, frameSize)
	bandE = ensureEnerSlice(&e.scratch.bandE, nbBands)
	NormalizeBandsToArrayIntoF32(mdctCoeffs, nbBands, frameSize, norm, bandE)
	if e.lfe {
		applyLFELinearBandEClamp(bandE, nbBands, 1)
		normalizeBandsWithBandEIntoF32(mdctCoeffs, nbBands, frameSize, norm, bandE)
	}
	return norm, bandE
}

// NormalizeBandsToArrayStereoWithBandE normalizes MDCT coefficients for stereo
// and returns normalized L/R coefficients plus combined linear band amplitudes.
// The bandE layout is [L bands][R bands].
func (e *Encoder) NormalizeBandsToArrayStereoWithBandE(mdctLeft, mdctRight []float32, nbBands, frameSize int) (normL, normR []celtNorm, bandE []celtEner) {
	normL = ensureNormSlice(&e.scratch.normL, frameSize)
	normR = ensureNormSlice(&e.scratch.normR, frameSize)
	bandEL := ensureEnerSlice(&e.scratch.bandEL, nbBands)
	bandER := ensureEnerSlice(&e.scratch.bandER, nbBands)
	NormalizeBandsToArrayInto(mdctLeft, nbBands, frameSize, normL, bandEL)
	NormalizeBandsToArrayInto(mdctRight, nbBands, frameSize, normR, bandER)
	bandE = ensureEnerSlice(&e.scratch.bandE, nbBands*2)
	copy(bandE[:nbBands], bandEL)
	copy(bandE[nbBands:], bandER)
	if e.lfe {
		applyLFELinearBandEClamp(bandE, nbBands, 2)
		normalizeBandsWithBandEInto(mdctLeft, nbBands, frameSize, normL, bandE[:nbBands])
		normalizeBandsWithBandEInto(mdctRight, nbBands, frameSize, normR, bandE[nbBands:])
	}
	return normL, normR, bandE
}

// NormalizeBandsToArrayStereoWithBandEF32 normalizes float-build MDCT
// coefficients for stereo. The bandE layout is [L bands][R bands].
func (e *Encoder) NormalizeBandsToArrayStereoWithBandEF32(mdctLeft, mdctRight []float32, nbBands, frameSize int) (normL, normR []celtNorm, bandE []celtEner) {
	normL = ensureNormSlice(&e.scratch.normL, frameSize)
	normR = ensureNormSlice(&e.scratch.normR, frameSize)
	bandEL := ensureEnerSlice(&e.scratch.bandEL, nbBands)
	bandER := ensureEnerSlice(&e.scratch.bandER, nbBands)
	NormalizeBandsToArrayIntoF32(mdctLeft, nbBands, frameSize, normL, bandEL)
	NormalizeBandsToArrayIntoF32(mdctRight, nbBands, frameSize, normR, bandER)
	bandE = ensureEnerSlice(&e.scratch.bandE, nbBands*2)
	copy(bandE[:nbBands], bandEL)
	copy(bandE[nbBands:], bandER)
	if e.lfe {
		applyLFELinearBandEClamp(bandE, nbBands, 2)
		normalizeBandsWithBandEIntoF32(mdctLeft, nbBands, frameSize, normL, bandE[:nbBands])
		normalizeBandsWithBandEIntoF32(mdctRight, nbBands, frameSize, normR, bandE[nbBands:])
	}
	return normL, normR, bandE
}

// normalizeBandsMonoBinMulF32 normalizes float-build mono MDCT coefficients
// using an explicit bin multiplier M=1<<LM (band edges eBands[i]*M). It mirrors
// NormalizeBandsToArrayMonoWithBandEF32 but takes the libopus M directly, for the
// native 96 kHz HD mode where M != frameSize/120.
func (e *Encoder) normalizeBandsMonoBinMulF32(mdctCoeffs []float32, nbBands, binMul int) (norm []celtNorm, bandE []celtEner) {
	frameSize := len(mdctCoeffs)
	norm = ensureNormSlice(&e.scratch.normL, frameSize)
	bandE = ensureEnerSlice(&e.scratch.bandE, nbBands)
	if pm := e.perMode; pm != nil {
		normalizeBandsToArrayIntoF32BinMulWidths(mdctCoeffs, nbBands, binMul, norm, bandE, pm.eBandWidths, pm.nbEBands)
		if e.lfe {
			applyLFELinearBandEClamp(bandE, nbBands, 1)
			normalizeBandsWithBandEIntoF32BinMulWidths(mdctCoeffs, nbBands, binMul, norm, bandE, pm.eBandWidths)
		}
		return norm, bandE
	}
	NormalizeBandsToArrayIntoF32BinMul(mdctCoeffs, nbBands, binMul, norm, bandE)
	if e.lfe {
		applyLFELinearBandEClamp(bandE, nbBands, 1)
		normalizeBandsWithBandEIntoF32BinMul(mdctCoeffs, nbBands, binMul, norm, bandE)
	}
	return norm, bandE
}

// normalizeBandsStereoBinMulF32 normalizes float-build stereo MDCT coefficients
// using an explicit bin multiplier M=1<<LM. The bandE layout is [L bands][R bands].
func (e *Encoder) normalizeBandsStereoBinMulF32(mdctLeft, mdctRight []float32, nbBands, binMul int) (normL, normR []celtNorm, bandE []celtEner) {
	frameSize := len(mdctLeft)
	normL = ensureNormSlice(&e.scratch.normL, frameSize)
	normR = ensureNormSlice(&e.scratch.normR, frameSize)
	bandEL := ensureEnerSlice(&e.scratch.bandEL, nbBands)
	bandER := ensureEnerSlice(&e.scratch.bandER, nbBands)
	if pm := e.perMode; pm != nil {
		// Non-standard per-mode custom layout: band edges come from the mode's
		// nbEBands band widths, not the static eBandWidths/MaxBands table.
		normalizeBandsToArrayIntoF32BinMulWidths(mdctLeft, nbBands, binMul, normL, bandEL, pm.eBandWidths, pm.nbEBands)
		normalizeBandsToArrayIntoF32BinMulWidths(mdctRight, nbBands, binMul, normR, bandER, pm.eBandWidths, pm.nbEBands)
		bandE = ensureEnerSlice(&e.scratch.bandE, nbBands*2)
		copy(bandE[:nbBands], bandEL)
		copy(bandE[nbBands:], bandER)
		if e.lfe {
			applyLFELinearBandEClamp(bandE, nbBands, 2)
			normalizeBandsWithBandEIntoF32BinMulWidths(mdctLeft, nbBands, binMul, normL, bandE[:nbBands], pm.eBandWidths)
			normalizeBandsWithBandEIntoF32BinMulWidths(mdctRight, nbBands, binMul, normR, bandE[nbBands:], pm.eBandWidths)
		}
		return normL, normR, bandE
	}
	NormalizeBandsToArrayIntoF32BinMul(mdctLeft, nbBands, binMul, normL, bandEL)
	NormalizeBandsToArrayIntoF32BinMul(mdctRight, nbBands, binMul, normR, bandER)
	bandE = ensureEnerSlice(&e.scratch.bandE, nbBands*2)
	copy(bandE[:nbBands], bandEL)
	copy(bandE[nbBands:], bandER)
	if e.lfe {
		applyLFELinearBandEClamp(bandE, nbBands, 2)
		normalizeBandsWithBandEIntoF32BinMul(mdctLeft, nbBands, binMul, normL, bandE[:nbBands])
		normalizeBandsWithBandEIntoF32BinMul(mdctRight, nbBands, binMul, normR, bandE[nbBands:])
	}
	return normL, normR, bandE
}

// TFResScratch returns a scratch TF resolution slice sized for nbBands.
func (e *Encoder) TFResScratch(nbBands int) []int32 {
	return ensureInt32Slice(&e.scratch.tfRes, nbBands)
}

// CapsScratch returns a scratch caps slice sized for nbBands.
func (e *Encoder) CapsScratch(nbBands int) []int32 {
	return ensureInt32Slice(&e.scratch.caps, nbBands)
}

// OffsetsScratch returns a scratch offsets slice sized for nbBands.
func (e *Encoder) OffsetsScratch(nbBands int) []int32 {
	return ensureInt32Slice(&e.scratch.offsets, nbBands)
}

// ComputeAllocationHybridScratch computes hybrid bit allocation, drawing its
// working buffers from the encoder scratch to avoid per-call allocations.
func (e *Encoder) ComputeAllocationHybridScratch(re *rangecoding.Encoder, totalBitsQ3, nbBands int, cap, offsets []int32, trim int, intensity int, dualStereo bool, lm int, prev int, signalBandwidth int) *AllocationResult {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	channels := max(e.codedChannels(), 1)
	if channels > 2 {
		channels = 2
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	result := &e.scratch.allocResult
	result.BandBits = ensureInt32Slice(&e.scratch.allocBits, nbBands)
	result.FineBits = ensureInt32Slice(&e.scratch.allocFineBits, nbBands)
	result.FinePriority = ensureInt32Slice(&e.scratch.allocFinePrio, nbBands)
	result.Caps = ensureInt32Slice(&e.scratch.allocCaps, nbBands)
	result.Balance = 0
	result.CodedBands = nbBands
	result.Intensity = 0
	result.DualStereo = false

	for i := 0; i < nbBands; i++ {
		result.BandBits[i] = 0
		result.FineBits[i] = 0
		result.FinePriority[i] = 0
		result.Caps[i] = 0
	}

	if nbBands == 0 || totalBitsQ3 <= 0 {
		return result
	}

	if cap == nil || len(cap) < nbBands {
		cap = initCaps(nbBands, lm, channels)
	}
	copy(result.Caps, cap[:nbBands])

	if offsets == nil {
		offsets = e.scratch.offsets[:nbBands]
		for i := range offsets {
			offsets[i] = 0
		}
	}

	intensityVal := intensity
	dualVal := 0
	if dualStereo {
		dualVal = 1
	}
	balance := 0
	pulses := result.BandBits
	fineBits := result.FineBits
	finePriority := result.FinePriority

	codedBands := cltComputeAllocationEncode(re, HybridCELTStartBand, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
		totalBitsQ3, &balance, pulses, fineBits, finePriority, channels, lm, prev, signalBandwidth)

	result.CodedBands = codedBands
	result.Balance = balance
	result.Intensity = intensityVal
	result.DualStereo = dualVal != 0

	return result
}

// SignalBandwidthForAllocation mirrors libopus signal-bandwidth gating used by
// clt_compute_allocation(). It combines analysis bandwidth with the equivalent
// bitrate-derived minimum bandwidth floor.
func (e *Encoder) SignalBandwidthForAllocation(nbBands, equivRate int) int {
	signalBandwidth := max(nbBands-1, 0)
	if e.analysisValid {
		minBandwidth := celtMinSignalBandwidth(equivRate, int(e.channels))
		signalBandwidth = max(e.analysisBandwidth, minBandwidth)
	}
	return signalBandwidth
}

// QuantAllBandsEncodeScratch encodes PVQ bands using the encoder's scratch buffers.
func (e *Encoder) QuantAllBandsEncodeScratch(re *rangecoding.Encoder, channels, frameSize, lm int, start, end int,
	normL, normR []celtNorm, pulses []int32, shortBlocks int, spread int, tapset int, dualStereo int, intensity int,
	tfRes []int32, totalBitsQ3 int, balance int, codedBands int, seed *uint32, complexity int, bandE []celtEner) {
	quantAllBandsEncodeScratch(
		re,
		channels,
		frameSize,
		lm,
		start,
		end,
		normL,
		normR,
		pulses,
		shortBlocks,
		spread,
		tapset,
		dualStereo,
		intensity,
		tfRes,
		totalBitsQ3,
		balance,
		codedBands,
		e.phaseInversionDisabled,
		seed,
		complexity,
		bandE,
		nil,
		nil,
		&e.bandEncScratch,
	)
}

// DecideStereoParams mirrors the stereo-mode parameter decision in
// celt_encode_with_ec (celt_encoder.c, the C==2 block right after dynalloc):
// it runs the intensity-band hysteresis decision against the persistent
// st->intensity state, clamps it to [start, end], and runs stereo_analysis for
// the dual_stereo flag (MS-only for 2.5 ms frames). The CELT-only encoder runs
// this inline; the hybrid encoder must call it so its high-band stereo coupling
// matches libopus instead of defaulting intensity to nbBands.
//
// Returns the chosen intensity band and dual_stereo flag. normL/normR are the
// normalised bands; equivRate is the libopus equiv_rate.
func (e *Encoder) DecideStereoParams(normL, normR []celtNorm, equivRate, lm, nbBands, start, end int) (intensity int, dualStereo bool) {
	dualStereo = false
	if lm != 0 {
		dualStereo = stereoAnalysisDecision(normL, normR, lm, nbBands)
	}
	e.intensity = int32(hysteresisDecisionInt(
		equivRate/1000,
		celtIntensityThresholds[:],
		celtIntensityHysteresis[:],
		int(e.intensity),
	))
	if int(e.intensity) < start {
		e.intensity = int32(start)
	}
	if int(e.intensity) > end {
		e.intensity = int32(end)
	}
	return int(e.intensity), dualStereo
}

// LastCodedBands returns the last coded band count used for allocation skip decisions.
func (e *Encoder) LastCodedBands() int {
	return int(e.lastCodedBands)
}

// SetLastCodedBands updates the last coded band count.
func (e *Encoder) SetLastCodedBands(val int) {
	e.lastCodedBands = int32(val)
}

// ConsecTransient returns the number of consecutive transient frames.
func (e *Encoder) ConsecTransient() int {
	return int(e.consecTransient)
}

// UpdateConsecTransient updates the consecutive transient counter.
func (e *Encoder) UpdateConsecTransient(transient bool) {
	e.UpdateConsecTransientWithDisabled(transient, false)
}

// UpdateConsecTransientWithDisabled mirrors libopus consec_transient state
// cadence when transients are disabled by bit budget.
func (e *Encoder) UpdateConsecTransientWithDisabled(transient bool, transientGotDisabled bool) {
	if transient {
		e.consecTransient++
	} else if transientGotDisabled {
		e.consecTransient++
	} else {
		e.consecTransient = 0
	}
}

// StabilizeEnergiesBeforeCoarseHybrid mirrors libopus pre-coarse stabilization:
// if abs(bandLogE-oldBandE) < 2, bias current energy toward previous quant error.
func (e *Encoder) StabilizeEnergiesBeforeCoarseHybrid(energies []celtGLog, start, end, nbBands int) {
	if nbBands <= 0 || len(energies) == 0 {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > nbBands {
		end = nbBands
	}
	if start >= end {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	channels := int(e.channels)
	for c := range channels {
		baseState := c * MaxBands
		baseFrame := c * nbBands
		for band := start; band < end; band++ {
			stateIdx := baseState + band
			frameIdx := baseFrame + band
			if frameIdx >= len(energies) || stateIdx >= len(e.energyError) || stateIdx >= len(e.prevEnergy) {
				continue
			}
			oldE := float32(e.prevEnergy[stateIdx])
			curE := float32(energies[frameIdx])
			diff := curE - oldE
			if diff < 0 {
				diff = -diff
			}
			if diff < 2.0 {
				energies[frameIdx] = celtGLog(curE - 0.25*float32(e.energyError[stateIdx]))
			}
		}
	}
}

// UpdateEnergyErrorHybrid mirrors libopus energyError cadence in hybrid mode:
// clear all bands, then store clipped post-finalise residuals for coded bands.
func (e *Encoder) UpdateEnergyErrorHybrid(energies, quantizedEnergies []celtGLog, start, end, nbBands int) {
	if len(e.energyError) == 0 || nbBands <= 0 {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > nbBands {
		end = nbBands
	}
	if end > MaxBands {
		end = MaxBands
	}
	if start >= end {
		for i := range e.energyError {
			e.energyError[i] = 0
		}
		return
	}

	// libopus clears energyError every frame before writing coded-band residuals.
	for i := range e.energyError {
		e.energyError[i] = 0
	}

	channels := int(e.channels)
	for c := range channels {
		baseState := c * MaxBands
		baseFrame := c * nbBands
		for band := start; band < end; band++ {
			stateIdx := baseState + band
			frameIdx := baseFrame + band
			if stateIdx >= len(e.energyError) || frameIdx >= len(energies) || frameIdx >= len(quantizedEnergies) {
				continue
			}
			err := energies[frameIdx] - quantizedEnergies[frameIdx]
			if err < -0.5 {
				err = -0.5
			} else if err > 0.5 {
				err = 0.5
			}
			e.energyError[stateIdx] = celtGLog(err)
		}
	}
}

// UpdateEnergyErrorHybridFromError mirrors libopus hybrid cadence exactly:
// clear all bands, then store clipped post-finalise residual error[] values for
// coded bands. Residuals come from scratch.coarseError updated by coarse/fine/final.
func (e *Encoder) UpdateEnergyErrorHybridFromError(start, end, nbBands int) {
	if len(e.energyError) == 0 || nbBands <= 0 {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > nbBands {
		end = nbBands
	}
	if end > MaxBands {
		end = MaxBands
	}

	for i := range e.energyError {
		e.energyError[i] = 0
	}
	if start >= end {
		return
	}

	channels := max(int(e.channels), 1)
	errorVals := ensureGLogSliceNoClear(&e.scratch.coarseError, nbBands*channels)

	for c := 0; c < channels; c++ {
		baseState := c * MaxBands
		baseFrame := c * nbBands
		for band := start; band < end; band++ {
			stateIdx := baseState + band
			frameIdx := baseFrame + band
			if stateIdx >= len(e.energyError) || frameIdx >= len(errorVals) {
				continue
			}
			err := float32(errorVals[frameIdx])
			if err < -0.5 {
				err = -0.5
			} else if err > 0.5 {
				err = 0.5
			}
			e.energyError[stateIdx] = celtGLog(err)
		}
	}
}

// ApplyHybridPrefilter mirrors libopus hybrid mode: run_prefilter() still runs
// with enabled=false so previous CELT postfilter state can fade out cleanly even
// though no prefilter header bits are signaled in hybrid packets.
func (e *Encoder) ApplyHybridPrefilter(preemph []float32, frameSize int, tfEstimate float32, nbAvailableBytes int, toneFreq, toneishness float32) {
	channels := int(e.channels)
	if frameSize <= 0 || channels <= 0 || len(preemph) < frameSize*channels {
		return
	}

	prefilterTapset := e.TapsetDecision()
	maxPitchRatio := float32(1.0)
	if e.analysisValid {
		maxPitchRatio = e.analysisMaxPitchRatio
	}

	prevPrefilterPeriod := e.prefilterPeriod
	prevPrefilterGain := e.prefilterGain
	pfResult := e.runPrefilter(preemph, frameSize, prefilterTapset, false, tfEstimate, nbAvailableBytes, toneFreq, toneishness, maxPitchRatio)

	e.lastPitchChange = false
	if prevPrefilterPeriod > 0 && (pfResult.gain > 0.4 || prevPrefilterGain > 0.4) {
		upper := 126 * prevPrefilterPeriod / 100
		lower := 79 * prevPrefilterPeriod / 100
		e.lastPitchChange = pfResult.pitch > upper || pfResult.pitch < lower
	}
}

// TransientAnalysisHybrid performs transient analysis and updates preemph overlap state.
// Returns transient flags, tf/tone metrics, shortBlocks choice, and optional bandLogE2.
func (e *Encoder) TransientAnalysisHybrid(preemph []float32, frameSize, nbBands, lm int, allowWeakTransients bool) (transient bool, weakTransient bool, tfEstimate, toneFreq, toneishness float32, shortBlocks int, bandLogE2 []celtGLog) {
	overlap := Overlap
	if overlap > frameSize {
		overlap = frameSize
	}

	channels := int(e.channels)
	preemphBufSize := overlap * channels
	transientLen := (overlap + frameSize) * channels

	var result TransientAnalysisResult
	if e.channels == 1 {
		transientInput := e.scratch.transientInput
		if len(transientInput) < transientLen {
			transientInput = make([]float32, transientLen)
			e.scratch.transientInput = transientInput
		}
		transientInput = transientInput[:transientLen]
		e.fillTransientHistoryFromPrefilterF32(overlap, transientInput[:preemphBufSize])
		copy(transientInput[preemphBufSize:], preemph)
		result = e.transientAnalysisMonoFloat32(transientInput, frameSize+overlap, allowWeakTransients)
	} else {
		transientInput := e.scratch.transientInput
		if len(transientInput) < transientLen {
			transientInput = make([]float32, transientLen)
			e.scratch.transientInput = transientInput
		}
		transientInput = transientInput[:transientLen]
		e.fillTransientHistoryFromPrefilterF32(overlap, transientInput[:preemphBufSize])
		copy(transientInput[preemphBufSize:], preemph)
		result = e.TransientAnalysisF32(transientInput, frameSize+overlap, allowWeakTransients)
	}
	transient = result.IsTransient
	weakTransient = result.WeakTransient
	tfEstimate = result.TfEstimate
	toneFreq = result.ToneFreq
	toneishness = result.Toneishness

	maxToneishness := 1.0 - tfEstimate
	if toneishness > maxToneishness {
		toneishness = maxToneishness
	}

	shortBlocks = 1
	if transient {
		mode := GetModeConfig(frameSize)
		shortBlocks = mode.ShortBlocks
	}

	secondMdct := shortBlocks > 1 && e.complexity >= 8
	if !secondMdct {
		return transient, weakTransient, tfEstimate, toneFreq, toneishness, shortBlocks, nil
	}

	if e.channels == 1 {
		hist := e.scratch.leftHist
		if len(hist) < overlap {
			hist = make([]float32, overlap)
			e.scratch.leftHist = hist
		}
		hist = hist[:overlap]
		copySigToFloat32(hist, e.overlapBuffer[:overlap])
		mdctLong := computeMDCTWithHistoryScratch(preemph, hist, 1, &e.scratch)
		bandLogE2 = ensureGLogSlice(&e.scratch.bandLogE2, nbBands*channels)
		computeBandEnergiesGLogF32Into(mdctLong, nbBands, frameSize, channels, 1<<lm, bandLogE2)
	} else {
		left, right := deinterleaveStereoScratchF32(preemph, &e.scratch.deintLeft, &e.scratch.deintRight)
		if len(e.overlapBuffer) < 2*overlap {
			newBuf := make([]celtSig, 2*overlap)
			if len(e.overlapBuffer) > 0 {
				copy(newBuf, e.overlapBuffer)
			}
			e.overlapBuffer = newBuf
		}
		leftHist := e.scratch.leftHist
		rightHist := e.scratch.rightHist
		if len(leftHist) < overlap {
			leftHist = make([]float32, overlap)
			e.scratch.leftHist = leftHist
		}
		if len(rightHist) < overlap {
			rightHist = make([]float32, overlap)
			e.scratch.rightHist = rightHist
		}
		leftHist = leftHist[:overlap]
		rightHist = rightHist[:overlap]
		copySigToFloat32(leftHist, e.overlapBuffer[:overlap])
		copySigToFloat32(rightHist, e.overlapBuffer[overlap:2*overlap])
		mdctLeftLong := computeMDCTWithHistoryScratchStereoL(left, leftHist, 1, &e.scratch)
		mdctRightLong := computeMDCTWithHistoryScratchStereoR(right, rightHist, 1, &e.scratch)
		mdctLongLen := len(mdctLeftLong) + len(mdctRightLong)
		mdctLong := e.scratch.mdctCoeffsF32
		if len(mdctLong) < mdctLongLen {
			mdctLong = make([]float32, mdctLongLen)
			e.scratch.mdctCoeffsF32 = mdctLong
		}
		mdctLong = mdctLong[:mdctLongLen]
		copy(mdctLong, mdctLeftLong)
		copy(mdctLong[len(mdctLeftLong):], mdctRightLong)
		bandLogE2 = ensureGLogSlice(&e.scratch.bandLogE2, nbBands*channels)
		computeBandEnergiesGLogF32Into(mdctLong, nbBands, frameSize, channels, 1<<lm, bandLogE2)
	}

	if bandLogE2 != nil {
		offset := celtGLog(0.5 * float32(lm))
		for i := range bandLogE2 {
			bandLogE2[i] += offset
		}
	}

	return transient, weakTransient, tfEstimate, toneFreq, toneishness, shortBlocks, bandLogE2
}

// DynallocAnalysisHybridScratch runs dynalloc analysis using encoder scratch buffers.
func (e *Encoder) DynallocAnalysisHybridScratch(bandLogE, bandLogE2 []celtGLog, oldBandE []celtGLog, nbBands, start, end, lsbDepth, lm int, effectiveBytes int, isTransient, vbr, constrainedVBR bool, toneFreq, toneishness float32) DynallocResult {
	if nbBands < 0 {
		nbBands = 0
	}
	if start < 0 {
		start = 0
	}
	if end > nbBands {
		end = nbBands
	}
	if end < start {
		end = start
	}

	logN := e.scratch.logN
	if len(logN) < nbBands {
		logN = make([]int16, nbBands)
		e.scratch.logN = logN
	}
	logN = logN[:nbBands]
	for i := 0; i < nbBands && i < len(LogN); i++ {
		logN[i] = int16(LogN[i])
	}

	result := DynallocAnalysisWithScratch(
		bandLogE,
		bandLogE2,
		oldBandE,
		nbBands,
		start,
		end,
		int(e.channels),
		lsbDepth,
		lm,
		logN,
		effectiveBytes,
		isTransient,
		vbr,
		constrainedVBR,
		e.lfe,
		toneFreq,
		toneishness,
		nil,
		e.analysisValid,
		e.dynallocLeakBoost(),
		&e.dynallocScratch,
	)
	e.lastDynalloc = result
	return result
}

// TFAnalysisHybridScratch runs TF analysis using the encoder's scratch buffers.
func (e *Encoder) TFAnalysisHybridScratch(norm []celtNorm, nbBands int, transient bool, lm int, tfEstimate opusVal16, effectiveBytes int, importance []int32) ([]int32, int) {
	return TFAnalysisWithScratch(norm, len(norm), nbBands, transient, lm, tfEstimate, effectiveBytes, importance, &e.tfScratch)
}

// UpdateTonalityAnalysisHybrid updates tonality metrics for VBR decisions.
func (e *Encoder) UpdateTonalityAnalysisHybrid(normCoeffs []celtNorm, energies []celtGLog, nbBands, frameSize int) {
	if !e.vbr {
		return
	}
	e.updateTonalityAnalysis(normCoeffs, energies, nbBands, frameSize)
}

// BitrateToBits exposes bitrate_to_bits for hybrid callers.
func (e *Encoder) BitrateToBits(frameSize int) int {
	return e.bitrateToBits(frameSize)
}

// CBRPayloadBytes exposes cbrPayloadBytes for hybrid callers.
func (e *Encoder) CBRPayloadBytes(frameSize int) int {
	return e.cbrPayloadBytes(frameSize)
}

// SetCoarseEnergyAvailableBytes overrides nbAvailableBytes used by coarse
// energy intra/decay logic. Use 0 to clear the override.
func (e *Encoder) SetCoarseEnergyAvailableBytes(bytes int) {
	if bytes < 0 {
		bytes = 0
	}
	e.coarseAvailableBytes = int32(bytes)
}

// MDCTScratch computes the MDCT using the encoder's pre-allocated scratch buffers.
// This is the zero-allocation equivalent of the public MDCT function.
// EnsureScratch must have been called with an appropriate frameSize first.
func (e *Encoder) MDCTScratch(samples []float32) []float32 {
	return mdctScratch(samples, &e.scratch)
}

// MDCTScratchF32 computes the MDCT from float-build input scratch.
func (e *Encoder) MDCTScratchF32(samples []float32) []float32 {
	return mdctScratchF32(samples, &e.scratch)
}

// MDCTScratchCoeffsF32 computes float-build MDCT coefficients without widening
// the runtime coefficient buffer.
func (e *Encoder) MDCTScratchCoeffsF32(samples []float32) []float32 {
	return mdctScratchF32Coeffs(samples, &e.scratch)
}

// MDCTShortScratch computes the short-block MDCT using scratch buffers.
// This is the zero-allocation equivalent of MDCTShort.
// EnsureScratch must have been called with an appropriate frameSize first.
func (e *Encoder) MDCTShortScratch(samples []float32, shortBlocks int) []float32 {
	return mdctShortScratch(samples, shortBlocks, &e.scratch)
}

// MDCTShortScratchF32 computes the short-block MDCT from float-build input scratch.
func (e *Encoder) MDCTShortScratchF32(samples []float32, shortBlocks int) []float32 {
	return mdctShortScratchF32(samples, shortBlocks, &e.scratch)
}

// MDCTShortScratchCoeffsF32 computes short-block float-build MDCT coefficients
// without widening the runtime coefficient buffer.
func (e *Encoder) MDCTShortScratchCoeffsF32(samples []float32, shortBlocks int) []float32 {
	return mdctShortScratchF32Coeffs(samples, shortBlocks, &e.scratch)
}

// ComputeMDCTWithHistoryScratch computes MDCT with history using scratch buffers.
// inputScratch is used to assemble [history|samples] before the transform.
// history is updated in-place with the current frame's tail.
// EnsureScratch must have been called first.
func (e *Encoder) ComputeMDCTWithHistoryScratch(inputScratch, samples, history []float32, shortBlocks int) []float32 {
	if len(samples) == 0 {
		return nil
	}

	overlap := Overlap
	if overlap > len(samples) {
		overlap = len(samples)
	}
	input := inputScratch[:len(samples)+overlap]

	// Copy history overlap into the head of the input buffer.
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(input[:overlap], history[len(history)-overlap:])
		} else {
			start := overlap - len(history)
			for i := range start {
				input[i] = 0
			}
			copy(input[start:overlap], history)
		}
	} else {
		for i := 0; i < overlap; i++ {
			input[i] = 0
		}
	}

	// Append current frame samples after the overlap.
	copy(input[overlap:], samples)

	// Update history with the current frame tail (overlap samples).
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(history, samples[len(samples)-overlap:])
		} else {
			copy(history, samples[len(samples)-len(history):])
		}
	}

	if shortBlocks > 1 {
		return mdctShortScratch(input, shortBlocks, &e.scratch)
	}
	return mdctScratch(input, &e.scratch)
}

// ComputeMDCTWithHistoryScratchStereoL computes MDCT for the left channel using
// separate scratch output buffers. The result is written to scratch.mdctLeftF32 so it
// survives a subsequent right-channel call.
func (e *Encoder) ComputeMDCTWithHistoryScratchStereoL(samples, history []float32, shortBlocks int) []float32 {
	return computeMDCTWithHistoryScratchStereoL(samples, history, shortBlocks, &e.scratch)
}

// ComputeMDCTWithHistoryScratchStereoR computes MDCT for the right channel using
// separate scratch output buffers. The result is written to scratch.mdctRightF32.
func (e *Encoder) ComputeMDCTWithHistoryScratchStereoR(samples, history []float32, shortBlocks int) []float32 {
	return computeMDCTWithHistoryScratchStereoR(samples, history, shortBlocks, &e.scratch)
}

// ApplyDCRejectScratchHybrid applies DC rejection using the encoder scratch buffers.
func (e *Encoder) ApplyDCRejectScratchHybrid(pcm []float32) []float32 {
	return e.applyDCRejectScratch(pcm)
}

// ApplyDelayCompensationScratchHybrid applies CELT delay compensation using
// float-build input storage. It prepends the delay buffer and returns a
// frame-sized slice of samples.
func (e *Encoder) ApplyDelayCompensationScratchHybrid(pcm []float32, frameSize int) []float32 {
	channels := int(e.channels)
	expectedLen := frameSize * channels
	delayComp := DelayCompensation * channels
	if len(e.delayBuffer) < delayComp {
		e.delayBuffer = make([]opusRes, delayComp)
	}

	combinedLen := delayComp + len(pcm)
	combinedBuf := ensureFloat32Slice(&e.scratch.combinedBufF32, combinedLen)
	for i := range delayComp {
		combinedBuf[i] = float32(e.delayBuffer[i])
	}
	copy(combinedBuf[delayComp:], pcm)

	samplesForFrame := combinedBuf[:expectedLen]
	delayTailStart := len(combinedBuf) - delayComp
	for i := range delayComp {
		e.delayBuffer[i] = opusRes(combinedBuf[delayTailStart+i])
	}

	return samplesForFrame
}

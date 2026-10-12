package celt

// normalizeBandsMonoF32 runs normalise_bands() on float-build mono MDCT
// coefficients and returns the normalized coefficients and the linear band
// amplitudes. amp, when non-nil, holds the compute_band_energies() amplitudes
// already computed for these coefficients.
func (e *Encoder) normalizeBandsMonoF32(mdctCoeffs []float32, nbBands, frameSize int, amp []celtEner) (norm []CeltNorm, bandE []CeltEner) {
	norm = ensureNormSliceNoClear(&e.scratch.normL, frameSize)
	bandE = ensureEnerSlice(&e.scratch.bandE, nbBands)
	if amp != nil {
		copy(bandE, amp[:nbBands])
		normalizeBandsWithBandEIntoF32(mdctCoeffs, nbBands, frameSize, norm, bandE)
	} else {
		NormalizeBandsToArrayIntoF32(mdctCoeffs, nbBands, frameSize, norm, bandE)
	}
	if e.lfe {
		applyLFELinearBandEClamp(bandE, nbBands, 1)
		normalizeBandsWithBandEIntoF32(mdctCoeffs, nbBands, frameSize, norm, bandE)
	}
	return norm, bandE
}

// normalizeBandsStereoF32 runs normalise_bands() on float-build stereo MDCT
// coefficients. The bandE layout is [L bands][R bands]; amp, when non-nil,
// holds the compute_band_energies() amplitudes already computed for these
// coefficients in that layout.
func (e *Encoder) normalizeBandsStereoF32(mdctLeft, mdctRight []float32, nbBands, frameSize int, amp []celtEner) (normL, normR []CeltNorm, bandE []CeltEner) {
	normL = ensureNormSliceNoClear(&e.scratch.normL, frameSize)
	normR = ensureNormSliceNoClear(&e.scratch.normR, frameSize)
	bandE = ensureEnerSlice(&e.scratch.bandE, nbBands*2)
	if amp != nil {
		copy(bandE, amp[:2*nbBands])
	} else {
		ComputeLinearBandAmplitudesIntoF32(mdctLeft, nbBands, frameSize, bandE[:nbBands])
		ComputeLinearBandAmplitudesIntoF32(mdctRight, nbBands, frameSize, bandE[nbBands:])
	}
	if e.lfe {
		applyLFELinearBandEClamp(bandE, nbBands, 2)
	}
	normalizeBandsWithBandEIntoF32(mdctLeft, nbBands, frameSize, normL, bandE[:nbBands])
	normalizeBandsWithBandEIntoF32(mdctRight, nbBands, frameSize, normR, bandE[nbBands:])
	return normL, normR, bandE
}

// normalizeBandsMonoBinMulF32 normalizes float-build mono MDCT coefficients
// using an explicit bin multiplier M=1<<LM (band edges eBands[i]*M). It mirrors
// normalizeBandsMonoF32 but takes the libopus M directly, for the
// native 96 kHz HD mode where M != frameSize/120.
func (e *Encoder) normalizeBandsMonoBinMulF32(mdctCoeffs []float32, nbBands, binMul int) (norm []celtNorm, bandE []celtEner) {
	frameSize := len(mdctCoeffs)
	norm = ensureNormSliceNoClear(&e.scratch.normL, frameSize)
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
	normL = ensureNormSliceNoClear(&e.scratch.normL, frameSize)
	normR = ensureNormSliceNoClear(&e.scratch.normR, frameSize)
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

// applyDelayCompensationScratch prepends the delay buffer of a standalone
// encoder to the frame and returns the frame-sized start of the result; the
// last DelayCompensation samples become the next delay buffer.
func (e *Encoder) applyDelayCompensationScratch(pcm []float32, frameSize int) []float32 {
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

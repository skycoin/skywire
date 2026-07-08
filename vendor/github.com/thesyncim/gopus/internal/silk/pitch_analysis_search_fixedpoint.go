//go:build gopus_fixed_point

package silk

// FIXED_POINT port of the SILK core pitch analyser silk_pitch_analysis_core
// from silk/fixed/pitch_analysis_core_FIX.c.
//
// This covers the STAGE-1 4 kHz decimated cross-correlation search and the
// STAGE-2 8 kHz normalized-correlation refinement, plus the STAGE-3 fine search
// (reusing the already-ported silkPAnaCalcCorrSt3Fixed and
// silkPAnaCalcEnergySt3Fixed kernels). It produces the same outputs as the
// reference: per-subframe pitch lags, lag/contour indices, the running
// normalized-correlation LTPCorr_Q15, and the voiced/unvoiced flag.

const (
	// SCRATCH layout / lag bounds at 4 kHz and 8 kHz, mirroring the C macros.
	peSFLength4kHz = peSubfrLengthMS * 4
	peSFLength8kHz = peSubfrLengthMS * 8
	peMinLag4kHz   = peMinLagMS * 4
	peMinLag8kHz   = peMinLagMS * 8
	peMaxLag4kHz   = peMaxLagMS * 4
	peMaxLag8kHz   = peMaxLagMS*8 - 1
	peCStride4kHz  = peMaxLag4kHz + 1 - peMinLag4kHz
	peCStride8kHz  = peMaxLag8kHz + 3 - (peMinLag8kHz - 2)
	peDCompMin     = peMinLag8kHz - 3
	peDCompMax     = peMaxLag8kHz + 4
	peDCompStride  = peDCompMax - peDCompMin
)

// silkInsertionSortDecreasingInt16 is the FIXED_POINT
// silk_insertion_sort_decreasing_int16: sorts the first K positions of a in
// decreasing order, recording the original indices in idx.
//
// NOTE(dedup): self-contained copy. If a shared fixed-point sort lands in the
// default silk build, fold this into it.
func silkInsertionSortDecreasingInt16(a []int16, idx []int, L, K int) {
	for i := 0; i < K; i++ {
		idx[i] = i
	}
	for i := 1; i < K; i++ {
		value := a[i]
		j := i - 1
		for ; j >= 0 && value > a[j]; j-- {
			a[j+1] = a[j]
			idx[j+1] = idx[j]
		}
		a[j+1] = value
		idx[j+1] = i
	}
	for i := K; i < L; i++ {
		value := a[i]
		if value > a[K-1] {
			j := K - 2
			for ; j >= 0 && value > a[j]; j-- {
				a[j+1] = a[j]
				idx[j+1] = idx[j]
			}
			a[j+1] = value
			idx[j+1] = i
		}
	}
}

// silkPitchAnalysisCoreFixed is the FIXED_POINT silk_pitch_analysis_core.
//
// frameUnscaled is the int16 analysis frame of length
// (PE_LTP_MEM_LENGTH_MS + nbSubfr*PE_SUBFR_LENGTH_MS) * fsKHz. pitchOut receives
// nbSubfr lag values. ltpCorrQ15 is read for the previous frame's normalized
// correlation and overwritten with the new value. prevLag is the previous
// frame's final lag (0 if unvoiced). Returns lagIndex, contourIndex and the
// voicing flag (0 voiced, 1 unvoiced).
func silkPitchAnalysisCoreFixed(
	sc *silkFixedEncodeScratch,
	frameUnscaled []int16,
	pitchOut []int,
	ltpCorrQ15 *int32,
	prevLag int,
	searchThres1Q16 int32,
	searchThres2Q13 int,
	fsKHz int,
	complexity int,
	nbSubfr int,
) (lagIndex int16, contourIndex int8, voicing int) {
	frameLength := (peLTPMemLengthMS + nbSubfr*peSubfrLengthMS) * fsKHz
	frameLength4kHz := (peLTPMemLengthMS + nbSubfr*peSubfrLengthMS) * 4
	frameLength8kHz := (peLTPMemLengthMS + nbSubfr*peSubfrLengthMS) * 8
	sfLength := peSubfrLengthMS * fsKHz
	minLag := peMinLagMS * fsKHz
	maxLag := peMaxLagMS*fsKHz - 1

	// Downscale input if necessary.
	energy, shift := silkSumSqrShiftFixed(frameUnscaled, frameLength)
	shift += 3 - int(silkCLZ32(energy)) // at least two bits headroom
	frameScaled := ensureInt16Slice(&sc.paFrameScaled, frameLength)
	var frame []int16
	if shift > 0 {
		shift = int(silkRSHIFT(int32(shift+1), 1))
		for i := 0; i < frameLength; i++ {
			frameScaled[i] = int16(silkRSHIFT(int32(frameUnscaled[i]), shift))
		}
		frame = frameScaled
	} else {
		frame = frameUnscaled
	}

	// Resample from input sampled at fsKHz to 8 kHz.
	var frame8kHz []int16
	switch fsKHz {
	case 16:
		frame8kHzBuf := ensureInt16Slice(&sc.paFrame8kHzBuf, frameLength8kHz)
		var filtState [2]int32
		n := resamplerDown2(&filtState, frame8kHzBuf, frame[:frameLength])
		frame8kHz = frame8kHzBuf[:n]
	case 12:
		frame8kHzBuf := ensureInt16Slice(&sc.paFrame8kHzBuf, frameLength8kHz)
		var filtState [6]int32
		scratch := ensureInt32Slice(&sc.paResScratch, frameLength+4)
		n := resamplerDown2_3(&filtState, frame8kHzBuf, frame[:frameLength], scratch)
		frame8kHz = frame8kHzBuf[:n]
	default: // 8 kHz
		frame8kHz = frame[:frameLength8kHz]
	}

	// Decimate again to 4 kHz.
	frame4kHz := ensureInt16Slice(&sc.paFrame4kHz, frameLength4kHz)
	var filtState4 [2]int32
	resamplerDown2(&filtState4, frame4kHz, frame8kHz[:frameLength8kHz])

	// Low-pass filter.
	for i := frameLength4kHz - 1; i > 0; i-- {
		frame4kHz[i] = silkAddSat16(frame4kHz[i], frame4kHz[i-1])
	}

	/*****************************************************************************
	 * FIRST STAGE, operating in 4 kHz
	 *****************************************************************************/
	c := ensureInt16Slice(&sc.paC, nbSubfr*peCStride8kHz)
	xcorr32 := ensureInt32Slice(&sc.paXcorr32, peMaxLag4kHz-peMinLag4kHz+1)

	target := silkLSHIFT(peSFLength4kHz, 2) // pointer into frame4kHz (middle of frame)
	targetIdx := int(target)
	for k := 0; k < nbSubfr>>1; k++ {
		basisIdx := targetIdx - peMinLag4kHz

		celtPitchXcorrFixed(frame4kHz[targetIdx:], frame4kHz[targetIdx-peMaxLag4kHz:],
			xcorr32, peSFLength8kHz, peMaxLag4kHz-peMinLag4kHz+1)

		crossCorr := xcorr32[peMaxLag4kHz-peMinLag4kHz]
		normalizer := silkInnerProdAlignedFixed(frame4kHz[targetIdx:], frame4kHz[targetIdx:], peSFLength8kHz)
		normalizer = silkADD32(normalizer, silkInnerProdAlignedFixed(frame4kHz[basisIdx:], frame4kHz[basisIdx:], peSFLength8kHz))
		normalizer = silkADD32(normalizer, silkSMULBB(peSFLength8kHz, 4000))

		c[k*peCStride4kHz+0] = int16(silk_DIV32_varQ(crossCorr, normalizer, 13+1)) // Q13

		for d := peMinLag4kHz + 1; d <= peMaxLag4kHz; d++ {
			basisIdx--
			crossCorr = xcorr32[peMaxLag4kHz-d]
			// Add contribution of new sample and remove contribution from oldest sample.
			normalizer = silkADD32(normalizer,
				silkSMULBB(int32(frame4kHz[basisIdx]), int32(frame4kHz[basisIdx]))-
					silkSMULBB(int32(frame4kHz[basisIdx+peSFLength8kHz]), int32(frame4kHz[basisIdx+peSFLength8kHz])))
			c[k*peCStride4kHz+(d-peMinLag4kHz)] = int16(silk_DIV32_varQ(crossCorr, normalizer, 13+1)) // Q13
		}
		targetIdx += peSFLength8kHz
	}

	// Combine two subframes into single correlation measure and apply short-lag bias.
	if nbSubfr == peMaxNbSubfr {
		for i := peMaxLag4kHz; i >= peMinLag4kHz; i-- {
			sum := int32(c[0*peCStride4kHz+(i-peMinLag4kHz)]) +
				int32(c[1*peCStride4kHz+(i-peMinLag4kHz)]) // Q14
			sum = silkSMLAWB(sum, sum, silkLSHIFT(int32(-i), 4)) // Q14
			c[i-peMinLag4kHz] = int16(sum)                       // Q14
		}
	} else {
		for i := peMaxLag4kHz; i >= peMinLag4kHz; i-- {
			sum := silkLSHIFT(int32(c[i-peMinLag4kHz]), 1)       // Q14
			sum = silkSMLAWB(sum, sum, silkLSHIFT(int32(-i), 4)) // Q14
			c[i-peMinLag4kHz] = int16(sum)                       // Q14
		}
	}

	// Sort.
	lengthDSrch := 4 + silkLSHIFT(int32(complexity), 1)
	dSrch := ensureIntSlice(&sc.paDSrch, peDSrchLength)
	silkInsertionSortDecreasingInt16(c, dSrch, peCStride4kHz, int(lengthDSrch))

	// Escape if correlation is very low already here.
	cMax := int(c[0]) // Q14
	if cMax < silkFixConst(0.2, 14) {
		for i := 0; i < nbSubfr; i++ {
			pitchOut[i] = 0
		}
		*ltpCorrQ15 = 0
		return 0, 0, 1
	}

	threshold := silkSMULWB(searchThres1Q16, int32(cMax))
	ldSrch := int(lengthDSrch)
	for i := 0; i < ldSrch; i++ {
		// Convert to 8 kHz indices for the sorted correlation that exceeds the threshold.
		if int32(c[i]) > threshold {
			dSrch[i] = int(silkLSHIFT(int32(dSrch[i]+peMinLag4kHz), 1))
		} else {
			ldSrch = i
			break
		}
	}

	dComp := ensureInt16Slice(&sc.paDComp, peDCompStride)
	for i := range dComp {
		dComp[i] = 0
	}
	for i := 0; i < ldSrch; i++ {
		dComp[dSrch[i]-peDCompMin] = 1
	}

	// Convolution.
	for i := peDCompMax - 1; i >= peMinLag8kHz; i-- {
		dComp[i-peDCompMin] += dComp[i-1-peDCompMin] + dComp[i-2-peDCompMin]
	}

	ldSrch = 0
	for i := peMinLag8kHz; i < peMaxLag8kHz+1; i++ {
		if dComp[i+1-peDCompMin] > 0 {
			dSrch[ldSrch] = i
			ldSrch++
		}
	}

	// Convolution.
	for i := peDCompMax - 1; i >= peMinLag8kHz; i-- {
		dComp[i-peDCompMin] += dComp[i-1-peDCompMin] + dComp[i-2-peDCompMin] + dComp[i-3-peDCompMin]
	}

	lengthDComp := 0
	for i := peMinLag8kHz; i < peDCompMax; i++ {
		if dComp[i-peDCompMin] > 0 {
			dComp[lengthDComp] = int16(i - 2)
			lengthDComp++
		}
	}

	/*****************************************************************************
	 * SECOND STAGE, operating at 8 kHz, on lag sections with high correlation
	 *****************************************************************************/
	for i := range c {
		c[i] = 0
	}

	targetIdx = peLTPMemLengthMS * 8
	for k := 0; k < nbSubfr; k++ {
		energyTarget := silkADD32(silkInnerProdAlignedFixed(frame8kHz[targetIdx:], frame8kHz[targetIdx:], peSFLength8kHz), 1)
		for j := 0; j < lengthDComp; j++ {
			d := int(dComp[j])
			basisIdx := targetIdx - d
			crossCorr := silkInnerProdAlignedFixed(frame8kHz[targetIdx:], frame8kHz[basisIdx:], peSFLength8kHz)
			if crossCorr > 0 {
				energyBasis := silkInnerProdAlignedFixed(frame8kHz[basisIdx:], frame8kHz[basisIdx:], peSFLength8kHz)
				c[k*peCStride8kHz+(d-(peMinLag8kHz-2))] =
					int16(silk_DIV32_varQ(crossCorr, silkADD32(energyTarget, energyBasis), 13+1)) // Q13
			} else {
				c[k*peCStride8kHz+(d-(peMinLag8kHz-2))] = 0
			}
		}
		targetIdx += peSFLength8kHz
	}

	// Search over lag range and lags codebook.
	cCmax := silkInt32Min
	cCmaxB := silkInt32Min

	cBimax := 0
	lag := -1

	var prevLagLog2Q7 int32
	if prevLag > 0 {
		if fsKHz == 12 {
			prevLag = int(silkDiv32_16(silkLSHIFT(int32(prevLag), 1), 3))
		} else if fsKHz == 16 {
			prevLag = int(silkRSHIFT(int32(prevLag), 1))
		}
		prevLagLog2Q7 = silkLin2Log(int32(prevLag))
	} else {
		prevLagLog2Q7 = 0
	}

	// Set up stage 2 codebook based on number of subframes.
	var lagCBStage2 [][]int8
	var nbCbkSearch int
	if nbSubfr == peMaxNbSubfr {
		lagCBStage2 = pitchCBLagsStage2Slice
		if fsKHz == 8 && complexity > SILK_PE_MIN_COMPLEX {
			nbCbkSearch = peNbCbksStage2Ext
		} else {
			nbCbkSearch = peNbCbksStage2
		}
	} else {
		lagCBStage2 = pitchCBLagsStage210msSlice
		nbCbkSearch = peNbCbksStage210ms
	}

	var cc [peNbCbksStage2Ext]int32
	for k := 0; k < ldSrch; k++ {
		d := dSrch[k]
		for j := 0; j < nbCbkSearch; j++ {
			cc[j] = 0
			for i := 0; i < nbSubfr; i++ {
				dSubfr := d + int(lagCBStage2[i][j])
				cc[j] += int32(c[i*peCStride8kHz+(dSubfr-(peMinLag8kHz-2))])
			}
		}
		// Find best codebook.
		cCmaxNew := silkInt32Min
		cBimaxNew := 0
		for i := 0; i < nbCbkSearch; i++ {
			if cc[i] > cCmaxNew {
				cCmaxNew = cc[i]
				cBimaxNew = i
			}
		}

		// Bias towards shorter lags.
		lagLog2Q7 := silkLin2Log(int32(d))                                                                            // Q7
		cCmaxNewB := cCmaxNew - silkRSHIFT(silkSMULBB(int32(nbSubfr*silkFixConst(peShortlagBias, 13)), lagLog2Q7), 7) // Q13

		// Bias towards previous lag.
		if prevLag > 0 {
			deltaLagLog2SqrQ7 := lagLog2Q7 - prevLagLog2Q7
			deltaLagLog2SqrQ7 = silkRSHIFT(silkSMULBB(deltaLagLog2SqrQ7, deltaLagLog2SqrQ7), 7)
			prevLagBiasQ13 := silkRSHIFT(silkSMULBB(int32(nbSubfr*silkFixConst(pePrevlagBias, 13)), *ltpCorrQ15), 15) // Q13
			prevLagBiasQ13 = silkDiv32(silkMUL(prevLagBiasQ13, deltaLagLog2SqrQ7), deltaLagLog2SqrQ7+int32(silkFixConst(0.5, 7)))
			cCmaxNewB -= prevLagBiasQ13 // Q13
		}

		if cCmaxNewB > cCmaxB && // Find maximum biased correlation
			cCmaxNew > silkSMULBB(int32(nbSubfr), int32(searchThres2Q13)) && // Correlation high enough to be voiced
			int(pitchCBLagsStage2[0][cBimaxNew]) <= peMinLag8kHz { // Lag must be in range
			cCmaxB = cCmaxNewB
			cCmax = cCmaxNew
			lag = d
			cBimax = cBimaxNew
		}
	}

	if lag == -1 {
		for i := 0; i < nbSubfr; i++ {
			pitchOut[i] = 0
		}
		*ltpCorrQ15 = 0
		return 0, 0, 1
	}

	// Output normalized correlation.
	*ltpCorrQ15 = silkLSHIFT(silkDiv32_16(cCmax, int32(nbSubfr)), 2)

	if fsKHz > 8 {
		// Search in original signal.
		cBimaxOld := cBimax
		// Compensate for decimation.
		if fsKHz == 12 {
			lag = int(silkRSHIFT(silkSMULBB(int32(lag), 3), 1))
		} else if fsKHz == 16 {
			lag = int(silkLSHIFT(int32(lag), 1))
		} else {
			lag = int(silkSMULBB(int32(lag), 3))
		}

		lag = silkLimitInt(lag, minLag, maxLag)
		startLag := silkMaxInt(lag-2, minLag)
		endLag := silkMinInt(lag+2, maxLag)
		lagNew := lag
		cBimax = 0

		cCmax = silkInt32Min
		// Pitch lags according to second stage.
		for k := 0; k < nbSubfr; k++ {
			pitchOut[k] = lag + 2*int(pitchCBLagsStage2[k][cBimaxOld])
		}

		// Set up codebook parameters according to complexity setting and frame length.
		var lagCBStage3 [][]int8
		if nbSubfr == peMaxNbSubfr {
			nbCbkSearch = pitchNbCbkSearchsStage3[complexity]
			lagCBStage3 = pitchCBLagsStage3Slice
		} else {
			nbCbkSearch = peNbCbksStage310ms
			lagCBStage3 = pitchCBLagsStage310msSlice
		}

		// Calculate the correlations and energies needed in stage 3.
		energiesSt3 := ensureStage3LagSlice(&sc.paEnergiesSt3, nbSubfr*nbCbkSearch)
		crossCorrSt3 := ensureStage3LagSlice(&sc.paCrossCorrSt3, nbSubfr*nbCbkSearch)
		silkPAnaCalcCorrSt3Fixed(crossCorrSt3, frame, startLag, sfLength, nbSubfr, complexity)
		silkPAnaCalcEnergySt3Fixed(energiesSt3, frame, startLag, sfLength, nbSubfr, complexity)

		lagCounter := 0
		contourBiasQ15 := silkDiv32_16(int32(silkFixConst(peFlatcontourBias, 15)), int32(lag))

		targetIdx = peLTPMemLengthMS * fsKHz
		energyTarget := silkADD32(silkInnerProdAlignedFixed(frame[targetIdx:], frame[targetIdx:], nbSubfr*sfLength), 1)
		for d := startLag; d <= endLag; d++ {
			for j := 0; j < nbCbkSearch; j++ {
				var crossCorr int32
				energyAcc := energyTarget
				for k := 0; k < nbSubfr; k++ {
					crossCorr = silkADD32(crossCorr, crossCorrSt3[k*nbCbkSearch+j][lagCounter])
					energyAcc = silkADD32(energyAcc, energiesSt3[k*nbCbkSearch+j][lagCounter])
				}
				var cCmaxNew int32
				if crossCorr > 0 {
					cCmaxNew = silk_DIV32_varQ(crossCorr, energyAcc, 13+1) // Q13
					// Reduce depending on flatness of contour.
					diff := silkInt16MAX - silkMUL(contourBiasQ15, int32(j)) // Q15
					cCmaxNew = silkSMULWB(cCmaxNew, diff)                    // Q14
				} else {
					cCmaxNew = 0
				}

				if cCmaxNew > cCmax && (d+int(pitchCBLagsStage3[0][j])) <= maxLag {
					cCmax = cCmaxNew
					lagNew = d
					cBimax = j
				}
			}
			lagCounter++
		}

		for k := 0; k < nbSubfr; k++ {
			pitchOut[k] = lagNew + int(lagCBStage3[k][cBimax])
			pitchOut[k] = silkLimitInt(pitchOut[k], minLag, peMaxLagMS*fsKHz)
		}
		lagIndex = int16(lagNew - minLag)
		contourIndex = int8(cBimax)
	} else { // fsKHz == 8
		// Save lags.
		for k := 0; k < nbSubfr; k++ {
			pitchOut[k] = lag + int(lagCBStage2[k][cBimax])
			pitchOut[k] = silkLimitInt(pitchOut[k], peMinLag8kHz, peMaxLagMS*8)
		}
		lagIndex = int16(lag - peMinLag8kHz)
		contourIndex = int8(cBimax)
	}
	return lagIndex, contourIndex, 0
}

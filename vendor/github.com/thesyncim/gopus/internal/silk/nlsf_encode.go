package silk

import "math"

// silkNLSFWeightsLaroia computes Laroia NLSF weights (Q2).
// Reference: libopus silk/NLSF_VQ_weights_laroia.c
func silkNLSFWeightsLaroia(wQ2 []int16, nlsfQ15 []int16, order int) {
	if order <= 0 || order&1 != 0 {
		return
	}
	if len(wQ2) < order || len(nlsfQ15) < order {
		return
	}

	tmp1 := silkMaxInt(int(nlsfQ15[0]), 1)
	tmp1 = int(silkDiv32_16(int32(1<<(15+nlsfWQ)), int32(tmp1)))
	tmp2 := silkMaxInt(int(nlsfQ15[1]-nlsfQ15[0]), 1)
	tmp2 = int(silkDiv32_16(int32(1<<(15+nlsfWQ)), int32(tmp2)))
	wQ2[0] = int16(silkMinInt(tmp1+tmp2, 32767))

	for k := 1; k < order-1; k += 2 {
		tmp1 = silkMaxInt(int(nlsfQ15[k+1]-nlsfQ15[k]), 1)
		tmp1 = int(silkDiv32_16(int32(1<<(15+nlsfWQ)), int32(tmp1)))
		wQ2[k] = int16(silkMinInt(tmp1+tmp2, 32767))

		tmp2 = silkMaxInt(int(nlsfQ15[k+2]-nlsfQ15[k+1]), 1)
		tmp2 = int(silkDiv32_16(int32(1<<(15+nlsfWQ)), int32(tmp2)))
		wQ2[k+1] = int16(silkMinInt(tmp1+tmp2, 32767))
	}

	tmp1 = silkMaxInt(int(int32(1<<15)-int32(nlsfQ15[order-1])), 1)
	tmp1 = int(silkDiv32_16(int32(1<<(15+nlsfWQ)), int32(tmp1)))
	wQ2[order-1] = int16(silkMinInt(tmp1+tmp2, 32767))
}

// silkNLSFVQ computes quantization error for each codebook vector (Q24).
// Reference: libopus silk/NLSF_VQ.c
func silkNLSFVQ(errQ24 []int32, inQ15 []int16, cbQ8 []uint8, cbWghtQ9 []int16, nVectors, order int) {
	if order <= 0 || order&1 != 0 {
		return
	}
	if len(errQ24) < nVectors || len(inQ15) < order {
		return
	}

	cbIdx := 0
	wIdx := 0
	for i := range nVectors {
		var sumErrQ24 int32
		var predQ24 int32
		for m := order - 2; m >= 0; m -= 2 {
			diffQ15 := int32(inQ15[m+1]) - (int32(cbQ8[cbIdx+m+1]) << 7)
			diffwQ24 := silkSMULBB(diffQ15, int32(cbWghtQ9[wIdx+m+1]))
			sumErrQ24 = silkAddSat32(sumErrQ24, silkAbs32(diffwQ24-(predQ24>>1)))
			predQ24 = diffwQ24

			diffQ15 = int32(inQ15[m]) - (int32(cbQ8[cbIdx+m]) << 7)
			diffwQ24 = silkSMULBB(diffQ15, int32(cbWghtQ9[wIdx+m]))
			sumErrQ24 = silkAddSat32(sumErrQ24, silkAbs32(diffwQ24-(predQ24>>1)))
			predQ24 = diffwQ24
		}
		errQ24[i] = sumErrQ24
		cbIdx += order
		wIdx += order
	}
}

// silkNLSFDelDecQuant performs delayed-decision quantization for NLSF residuals.
// Returns RD value in Q25 and fills indices with residual indices.
// Reference: libopus silk/NLSF_del_dec_quant.c
func silkNLSFDelDecQuant(indices []int8, xQ10 []int16, wQ5 []int16, predQ8 []uint8, ecIx []int16,
	ecRatesQ5 []uint8, quantStepSizeQ16 int16, invQuantStepSizeQ6 int16, muQ20 int32, order int) int32 {
	if order <= 0 || len(indices) < order {
		return 0
	}

	var ind [nlsfQuantDelDecStates][maxLPCOrder]int8
	var prevOutQ10 [2 * nlsfQuantDelDecStates]int16
	var rdQ25 [2 * nlsfQuantDelDecStates]int32
	var rdMinQ25 [nlsfQuantDelDecStates]int32
	var rdMaxQ25 [nlsfQuantDelDecStates]int32
	var indSort [nlsfQuantDelDecStates]int
	var out0Table [2 * nlsfQuantMaxAmplitudeExt]int32
	var out1Table [2 * nlsfQuantMaxAmplitudeExt]int32

	qssQ16 := int32(quantStepSizeQ16)

	for i := -nlsfQuantMaxAmplitudeExt; i <= nlsfQuantMaxAmplitudeExt-1; i++ {
		out0Q10 := int32(i) << 10
		out1Q10 := out0Q10 + 1024
		if i > 0 {
			out0Q10 -= nlsfQuantLevelAdjQ10
			out1Q10 -= nlsfQuantLevelAdjQ10
		} else if i == 0 {
			out1Q10 -= nlsfQuantLevelAdjQ10
		} else if i == -1 {
			out0Q10 += nlsfQuantLevelAdjQ10
		} else {
			out0Q10 += nlsfQuantLevelAdjQ10
			out1Q10 += nlsfQuantLevelAdjQ10
		}
		// Inline silkRSHIFT(silkSMULBB(outQ10, qss), 16).
		// out0Q10/out1Q10 are in [-10138, 10142], fit int16; qssQ16 already truncated.
		idx := i + nlsfQuantMaxAmplitudeExt
		out0Table[idx] = (int32(int16(out0Q10)) * qssQ16) >> 16
		out1Table[idx] = (int32(int16(out1Q10)) * qssQ16) >> 16
	}

	invQssQ6 := int32(invQuantStepSizeQ6)
	// Pre-truncate muQ20 to int16 once for silkSMLABB.
	muQ20_16 := int32(int16(muQ20))

	// BCE hints for slice accesses in the main loop.
	_ = xQ10[0]
	_ = wQ5[0]
	_ = predQ8[0]
	_ = ecIx[0]
	if order > 0 {
		_ = xQ10[order-1]
		_ = wQ5[order-1]
		_ = predQ8[order-1]
		_ = ecIx[order-1]
	}

	nStates := 1
	rdQ25[0] = 0
	prevOutQ10[0] = 0
	for i := order - 1; i >= 0; i-- {
		rates := ecRatesQ5[ecIx[i]:]
		inQ10 := int32(xQ10[i])
		predQ8i := int32(predQ8[i])
		wQ5i := int32(wQ5[i])

		for j := 0; j < nStates; j++ {
			// Inline: predQ10 = silkRSHIFT(silkSMULBB(predQ8[i], prevOutQ10[j]), 8)
			// predQ8[i] is uint8 (0-255), prevOutQ10[j] is int16 — both fit int16, no truncation needed.
			predQ10 := (predQ8i * int32(prevOutQ10[j])) >> 8
			resQ10 := inQ10 - predQ10

			// Inline: indTmp = silkRSHIFT(silkSMULBB(invQssQ6, resQ10), 16)
			// invQssQ6 already truncated. resQ10 gets truncated to int16.
			indTmp := int((invQssQ6 * int32(int16(resQ10))) >> 16)

			// Inline silkLimitInt (clamp).
			if indTmp < -nlsfQuantMaxAmplitudeExt {
				indTmp = -nlsfQuantMaxAmplitudeExt
			} else if indTmp > nlsfQuantMaxAmplitudeExt-1 {
				indTmp = nlsfQuantMaxAmplitudeExt - 1
			}
			ind[j][i] = int8(indTmp)

			tableIdx := indTmp + nlsfQuantMaxAmplitudeExt
			out0Q10 := out0Table[tableIdx] + predQ10
			out1Q10 := out1Table[tableIdx] + predQ10
			prevOutQ10[j] = int16(out0Q10)
			prevOutQ10[j+nStates] = int16(out1Q10)

			// Rate lookup — indTmp is in [-10, 9], nlsfQuantMaxAmplitude = 4.
			var rate0Q5, rate1Q5 int32
			indTmpPlus1 := indTmp + 1
			if indTmpPlus1 >= nlsfQuantMaxAmplitude {
				if indTmpPlus1 == nlsfQuantMaxAmplitude {
					rate0Q5 = int32(rates[indTmp+nlsfQuantMaxAmplitude])
					rate1Q5 = 280
				} else {
					rate0Q5 = 280 + 43*int32(indTmp-nlsfQuantMaxAmplitude)
					rate1Q5 = rate0Q5 + 43
				}
			} else if indTmp <= -nlsfQuantMaxAmplitude {
				if indTmp == -nlsfQuantMaxAmplitude {
					rate0Q5 = 280
					rate1Q5 = int32(rates[indTmpPlus1+nlsfQuantMaxAmplitude])
				} else {
					rate0Q5 = 280 - 43*int32(nlsfQuantMaxAmplitude+indTmp)
					rate1Q5 = rate0Q5 - 43
				}
			} else {
				rateIdx := indTmp + nlsfQuantMaxAmplitude
				rate0Q5 = int32(rates[rateIdx])
				rate1Q5 = int32(rates[rateIdx+1])
			}

			// RD computation — inline silkSMLABB(silkMLA(rdTmp, silkSMULBB(diff, diff), wQ5i), muQ20, rate).
			// silkSMULBB(diff, diff) = int32(int16(diff)) * int32(int16(diff))
			// silkMLA(a, b, c) = a + b*c
			// silkSMLABB(a, b, c) = a + int32(int16(b))*int32(int16(c))
			rdTmp := rdQ25[j]
			diffQ10 := int32(int16(inQ10 - out0Q10))
			rdQ25[j] = rdTmp + diffQ10*diffQ10*wQ5i + muQ20_16*int32(int16(rate0Q5))

			diffQ10 = int32(int16(inQ10 - out1Q10))
			rdQ25[j+nStates] = rdTmp + diffQ10*diffQ10*wQ5i + muQ20_16*int32(int16(rate1Q5))
		}

		if nStates <= nlsfQuantDelDecStates/2 {
			for j := 0; j < nStates; j++ {
				ind[j+nStates][i] = ind[j][i] + 1
			}
			nStates <<= 1
			for j := nStates; j < nlsfQuantDelDecStates; j++ {
				ind[j][i] = ind[j-nStates][i]
			}
		} else {
			// Sort/prune: for each state pair, put min in [j], max in [j+N].
			for j := range nlsfQuantDelDecStates {
				rdLo := rdQ25[j]
				rdHi := rdQ25[j+nlsfQuantDelDecStates]
				if rdLo > rdHi {
					rdQ25[j] = rdHi
					rdQ25[j+nlsfQuantDelDecStates] = rdLo
					rdMinQ25[j] = rdHi
					rdMaxQ25[j] = rdLo
					out0 := prevOutQ10[j]
					prevOutQ10[j] = prevOutQ10[j+nlsfQuantDelDecStates]
					prevOutQ10[j+nlsfQuantDelDecStates] = out0
					indSort[j] = j + nlsfQuantDelDecStates
				} else {
					rdMinQ25[j] = rdLo
					rdMaxQ25[j] = rdHi
					indSort[j] = j
				}
			}
			for {
				minMaxQ25 := int32(math.MaxInt32)
				maxMinQ25 := int32(0)
				indMinMax := 0
				indMaxMin := 0
				for j := range nlsfQuantDelDecStates {
					if minMaxQ25 > rdMaxQ25[j] {
						minMaxQ25 = rdMaxQ25[j]
						indMinMax = j
					}
					if maxMinQ25 < rdMinQ25[j] {
						maxMinQ25 = rdMinQ25[j]
						indMaxMin = j
					}
				}
				if minMaxQ25 >= maxMinQ25 {
					break
				}
				indSort[indMaxMin] = indSort[indMinMax] ^ nlsfQuantDelDecStates
				rdQ25[indMaxMin] = rdQ25[indMinMax+nlsfQuantDelDecStates]
				prevOutQ10[indMaxMin] = prevOutQ10[indMinMax+nlsfQuantDelDecStates]
				rdMinQ25[indMaxMin] = 0
				rdMaxQ25[indMinMax] = math.MaxInt32
				ind[indMaxMin] = ind[indMinMax]
			}
			for j := range nlsfQuantDelDecStates {
				ind[j][i] += int8(indSort[j] >> nlsfQuantDelDecStatesLog2)
			}
		}
	}

	indTmp := 0
	minQ25 := int32(math.MaxInt32)
	for j := range 2 * nlsfQuantDelDecStates {
		if minQ25 > rdQ25[j] {
			minQ25 = rdQ25[j]
			indTmp = j
		}
	}

	bestInd := &ind[indTmp&(nlsfQuantDelDecStates-1)]
	for j := range order {
		indices[j] = bestInd[j]
	}
	indices[0] += int8(indTmp >> nlsfQuantDelDecStatesLog2)
	return minQ25
}

// silkInsertionSortIncreasing matches libopus silk_insertion_sort_increasing().
// It sorts only the K lowest values in ascending order and records their original indices.
func silkInsertionSortIncreasing(a []int32, idx []int, L, K int) {
	if K <= 0 || L <= 0 || L < K {
		return
	}

	for i := range K {
		idx[i] = i
	}

	for i := 1; i < K; i++ {
		value := a[i]
		j := i - 1
		for ; j >= 0 && value < a[j]; j-- {
			a[j+1] = a[j]
			idx[j+1] = idx[j]
		}
		a[j+1] = value
		idx[j+1] = i
	}

	for i := K; i < L; i++ {
		value := a[i]
		if value < a[K-1] {
			j := K - 2
			for ; j >= 0 && value < a[j]; j-- {
				a[j+1] = a[j]
				idx[j+1] = idx[j]
			}
			a[j+1] = value
			idx[j+1] = i
		}
	}
}

// computeNLSFMuQ20 returns the NLSF_mu parameter (Q20) based on speech activity.
func computeNLSFMuQ20(speechActivityQ8 int, numSubframes int) int32 {
	// Match libopus:
	// SILK_FIX_CONST(0.003, 20)  -> 3146
	// SILK_FIX_CONST(-0.001, 28) -> -268434
	// The negative constant is biased toward zero because SILK_FIX_CONST adds
	// 0.5 before truncating the C cast.
	const nlsfMuBaseQ20 int32 = 3146
	const nlsfMuSlopeQ28 int32 = -268434

	mu := silkSMLAWB(nlsfMuBaseQ20, nlsfMuSlopeQ28, int32(speechActivityQ8))
	if numSubframes == 2 {
		mu = silkADD_RSHIFT32(mu, mu, 1)
	}
	if mu < 1 {
		mu = 1
	}
	return mu
}

// nlsfEncode performs MSVQ-based NLSF encoding aligned with libopus.
// Returns stage1 index, residual indices (length = order), and RD_Q25.
func (e *Encoder) nlsfEncode(nlsfQ15 []int16, cb *nlsfCB, wQ2 []int16, muQ20 int32, nSurvivors int, signalType int) (int, []int32, int32) {
	order := int(cb.order)
	nVectors := int(cb.nVectors)
	if len(nlsfQ15) < order {
		residuals := ensureInt32Slice(&e.scratchLsfResiduals, order)
		for i := range residuals {
			residuals[i] = 0
		}
		return 0, residuals, 0
	}

	// Match libopus silk_NLSF_encode: stabilize inside the encoder path,
	// after weights are prepared by process_NLSFs.
	silkNLSFStabilize(nlsfQ15[:order], cb.deltaMinQ15, order)

	var errQ24 [32]int32
	silkNLSFVQ(errQ24[:nVectors], nlsfQ15, cb.cb1NLSFQ8, cb.cb1WghtQ9, nVectors, order)

	if nSurvivors > nVectors {
		nSurvivors = nVectors
	}
	var tempIndices1 [32]int
	silkInsertionSortIncreasing(errQ24[:nVectors], tempIndices1[:nSurvivors], nVectors, nSurvivors)

	var resQ10 [maxLPCOrder]int16
	var wAdjQ5 [maxLPCOrder]int16
	var ecIx [maxLPCOrder]int16
	var predQ8 [maxLPCOrder]uint8
	var tmpIndices [maxLPCOrder]int8
	var rdQ25 [32]int32
	var tempIndices2 [32][maxLPCOrder]int8

	for s := 0; s < nSurvivors; s++ {
		ind1 := tempIndices1[s]
		baseIdx := ind1 * order

		for i := range order {
			wTmpQ9 := int32(cb.cb1WghtQ9[baseIdx+i])
			diff := int32(nlsfQ15[i]) - (int32(cb.cb1NLSFQ8[baseIdx+i]) << 7)
			resQ10[i] = int16(silkRSHIFT(silkSMULBB(diff, wTmpQ9), 14))
			denom := silkSMULBB(wTmpQ9, wTmpQ9)
			if denom == 0 {
				denom = 1
			}
			wAdjQ5[i] = int16(silk_DIV32_varQ(int32(wQ2[i]), denom, 21))
		}

		silkNLSFUnpack(ecIx[:order], predQ8[:order], cb, ind1)
		rdValQ25 := silkNLSFDelDecQuant(tmpIndices[:], resQ10[:], wAdjQ5[:], predQ8[:], ecIx[:], cb.ecRatesQ5, cb.quantStepSizeQ16, cb.invQuantStepSizeQ6, muQ20, order)

		// Add rate for first stage
		icdf := cb.cb1ICDF[(signalType>>1)*nVectors:]
		var probQ8 int32
		if ind1 == 0 {
			probQ8 = 256 - int32(icdf[0])
		} else {
			probQ8 = int32(icdf[ind1-1]) - int32(icdf[ind1])
		}
		bitsQ7 := int32((8 << 7)) - silkLin2Log(probQ8)
		rdValQ25 = silkSMLABB(rdValQ25, bitsQ7, muQ20>>2)
		rdQ25[s] = rdValQ25
		copy(tempIndices2[s][:order], tmpIndices[:order])
	}

	var bestIndex [1]int
	silkInsertionSortIncreasing(rdQ25[:nSurvivors], bestIndex[:], nSurvivors, 1)
	bestStage1 := tempIndices1[bestIndex[0]]

	residuals := ensureInt32Slice(&e.scratchLsfResiduals, order)
	var indices [maxLPCOrder + 1]int8
	indices[0] = int8(bestStage1)
	for i := range order {
		idx := tempIndices2[bestIndex[0]][i]
		residuals[i] = int32(idx)
		indices[i+1] = idx
	}
	silkNLSFDecode(nlsfQ15[:order], indices[:order+1], cb)

	return bestStage1, residuals, rdQ25[0]
}

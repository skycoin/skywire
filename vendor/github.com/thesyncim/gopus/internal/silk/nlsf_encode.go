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

	in := inQ15[:order]
	for i := range nVectors {
		cb := cbQ8[i*order : (i+1)*order][:len(in)]
		w := cbWghtQ9[i*order : (i+1)*order][:len(in)]
		var sumErrQ24 int32
		var predQ24 int32
		for m := len(in) - 2; m >= 0; m -= 2 {
			diffQ15 := int32(in[m+1]) - (int32(cb[m+1]) << 7)
			diffwQ24 := silkSMULBB(diffQ15, int32(w[m+1]))
			sumErrQ24 = silkAddSat32(sumErrQ24, silkAbs32(diffwQ24-(predQ24>>1)))
			predQ24 = diffwQ24

			diffQ15 = int32(in[m]) - (int32(cb[m]) << 7)
			diffwQ24 = silkSMULBB(diffQ15, int32(w[m]))
			sumErrQ24 = silkAddSat32(sumErrQ24, silkAbs32(diffwQ24-(predQ24>>1)))
			predQ24 = diffwQ24
		}
		errQ24[i] = sumErrQ24
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
	const (
		states = nlsfQuantDelDecStates
		ampExt = nlsfQuantMaxAmplitudeExt
		amp    = nlsfQuantMaxAmplitude
	)

	var ind [states][maxLPCOrder]int8
	var t nlsfDelDecTables
	prevOutQ10 := &t.prevOutQ10
	rdQ25 := &t.rdQ25
	var rdMinQ25 [states]int32
	var rdMaxQ25 [states]int32
	var indSort [states]int

	qssQ16 := int32(quantStepSizeQ16)

	for i := -ampExt; i <= ampExt-1; i++ {
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
		// silk_RSHIFT(silk_SMULBB(outQ10, quant_step_size_Q16), 16); the
		// outputs lie in [-10138, 10142] and fit int16.
		idx := i + ampExt
		t.out0Q10[idx] = (int32(int16(out0Q10)) * qssQ16) >> 16
		t.out1Q10[idx] = (int32(int16(out1Q10)) * qssQ16) >> 16
	}

	// t.rateQ5[m+ampExt] is the rate of index m. The inner entries,
	// |m| < amp, are rates_Q5[m+amp] of the current coefficient.
	t.rateQ5 = nlsfDelDecEscapeRatesQ5
	inner := t.rateQ5[ampExt-amp+1 : ampExt+amp]

	t.invQssQ6 = int32(invQuantStepSizeQ6)
	// silk_SMLABB reads mu_Q20 as int16.
	t.muQ20 = int32(int16(muQ20))

	xQ10 = xQ10[:order]
	wQ5 = wQ5[:order]
	predQ8 = predQ8[:order]
	ecIx = ecIx[:order]

	nStates := 1
	for i := order - 1; i >= 0; i-- {
		rates := ecRatesQ5[ecIx[i]+1:][:len(inner)]
		for k, r := range rates {
			inner[k] = int32(r)
		}
		nlsfDelDecStep(&t, &ind, nStates, i, int32(xQ10[i]), int32(predQ8[i]), int32(wQ5[i]))

		if nStates <= states/2 {
			for j := range nStates {
				ind[j+nStates][i] = ind[j][i] + 1
			}
			nStates <<= 1
			for j := nStates; j < states; j++ {
				ind[j][i] = ind[j-nStates][i]
			}
			continue
		}

		// Sort/prune: for each state pair, put min in [j], max in [j+N].
		// The selections are independent conditional assignments so they
		// compile to conditional moves; the RD comparisons are
		// data-dependent and mispredict as branches.
		for j := range states {
			rdLo := rdQ25[j]
			rdHi := rdQ25[j+states]
			out0 := prevOutQ10[j]
			out1 := prevOutQ10[j+states]
			swap := rdLo > rdHi
			sorted := j
			if swap {
				sorted = j + states
			}
			if swap {
				rdLo, rdHi = rdHi, rdLo
			}
			if swap {
				out0, out1 = out1, out0
			}
			rdQ25[j] = rdLo
			rdQ25[j+states] = rdHi
			rdMinQ25[j] = rdLo
			rdMaxQ25[j] = rdHi
			prevOutQ10[j] = out0
			prevOutQ10[j+states] = out1
			indSort[j] = sorted
		}
		for {
			// The lowest RD of the losing half and the highest of the
			// winning half, each at its first index as in the C scan.
			minMaxQ25, indMinMax := nlsfDelDecMin4(&rdMaxQ25)
			maxMinQ25, indMaxMin := nlsfDelDecMax4(&rdMinQ25)
			if minMaxQ25 >= maxMinQ25 {
				break
			}
			indSort[indMaxMin] = indSort[indMinMax] ^ states
			rdQ25[indMaxMin] = rdQ25[indMinMax+states]
			prevOutQ10[indMaxMin] = prevOutQ10[indMinMax+states]
			rdMinQ25[indMaxMin] = 0
			rdMaxQ25[indMinMax] = math.MaxInt32
			ind[indMaxMin] = ind[indMinMax]
		}
		// Indices from the upper half move up one level.
		ind[0][i] += int8(indSort[0] >> nlsfQuantDelDecStatesLog2)
		ind[1][i] += int8(indSort[1] >> nlsfQuantDelDecStatesLog2)
		ind[2][i] += int8(indSort[2] >> nlsfQuantDelDecStatesLog2)
		ind[3][i] += int8(indSort[3] >> nlsfQuantDelDecStatesLog2)
	}

	indTmp := 0
	minQ25 := int32(math.MaxInt32)
	for j := range 2 * states {
		if minQ25 > rdQ25[j] {
			minQ25 = rdQ25[j]
			indTmp = j
		}
	}

	bestInd := &ind[indTmp&(states-1)]
	copy(indices[:order], bestInd[:order])
	indices[0] += int8(indTmp >> nlsfQuantDelDecStatesLog2)
	return minQ25
}

// nlsfDelDecEscapeRatesQ5 holds the rate of each quantization index m in
// [-NLSF_QUANT_MAX_AMPLITUDE_EXT, NLSF_QUANT_MAX_AMPLITUDE_EXT] at m+10 for
// the indices outside the entropy-coded range: 280+43*(|m|-4) for |m| >= 4,
// the values silk_NLSF_del_dec_quant's rate branches compute. rate0 and rate1
// of an index ind_tmp are the entries of ind_tmp and ind_tmp+1.
var nlsfDelDecEscapeRatesQ5 = func() (r [2*nlsfQuantMaxAmplitudeExt + 1]int32) {
	for m := -nlsfQuantMaxAmplitudeExt; m <= nlsfQuantMaxAmplitudeExt; m++ {
		if m >= nlsfQuantMaxAmplitude {
			r[m+nlsfQuantMaxAmplitudeExt] = 280 - 43*nlsfQuantMaxAmplitude + 43*int32(m)
		} else if m <= -nlsfQuantMaxAmplitude {
			r[m+nlsfQuantMaxAmplitudeExt] = 280 - 43*nlsfQuantMaxAmplitude - 43*int32(m)
		}
	}
	return r
}()

// nlsfDelDecTables is the per-call state of silkNLSFDelDecQuant that the
// per-coefficient step reads and updates.
type nlsfDelDecTables struct {
	prevOutQ10 [2 * nlsfQuantDelDecStates]int16
	rdQ25      [2 * nlsfQuantDelDecStates]int32
	out0Q10    [2 * nlsfQuantMaxAmplitudeExt]int32
	out1Q10    [2 * nlsfQuantMaxAmplitudeExt]int32
	rateQ5     [2*nlsfQuantMaxAmplitudeExt + 1]int32
	invQssQ6   int32
	muQ20      int32
}

// nlsfDelDecStep quantizes coefficient i for each of the nStates survivors,
// writing the two candidate outputs of state j to j and j+nStates: the inner
// loop of silk_NLSF_del_dec_quant.
func nlsfDelDecStep(t *nlsfDelDecTables, ind *[nlsfQuantDelDecStates][maxLPCOrder]int8, nStates, i int, inQ10, predQ8, wQ5 int32) {
	const ampExt = nlsfQuantMaxAmplitudeExt
	for j := range nStates {
		// silk_SMULBB(pred_coef_Q8[i], prev_out_Q10[j]) >> 8.
		predQ10 := (predQ8 * int32(t.prevOutQ10[j])) >> 8
		resQ10 := inQ10 - predQ10

		// silk_SMULBB(inv_quant_step_size_Q6, res_Q10) >> 16, limited.
		indTmp := int((t.invQssQ6 * int32(int16(resQ10))) >> 16)
		indTmp = min(max(indTmp, -ampExt), ampExt-1)
		ind[j][i] = int8(indTmp)

		tableIdx := indTmp + ampExt
		out0Q10 := t.out0Q10[tableIdx] + predQ10
		out1Q10 := t.out1Q10[tableIdx] + predQ10
		t.prevOutQ10[j] = int16(out0Q10)
		t.prevOutQ10[j+nStates] = int16(out1Q10)

		// silk_SMLABB(silk_MLA(rd, silk_SMULBB(diff, diff), w_Q5[i]), mu_Q20, rate).
		rdTmp := t.rdQ25[j]
		diffQ10 := int32(int16(inQ10 - out0Q10))
		t.rdQ25[j] = rdTmp + diffQ10*diffQ10*wQ5 + t.muQ20*int32(int16(t.rateQ5[tableIdx]))

		diffQ10 = int32(int16(inQ10 - out1Q10))
		t.rdQ25[j+nStates] = rdTmp + diffQ10*diffQ10*wQ5 + t.muQ20*int32(int16(t.rateQ5[tableIdx+1]))
	}
}

// nlsfDelDecMin4 returns the smallest of v below math.MaxInt32 and its first
// index, or (math.MaxInt32, 0) when none is: the min_max_Q25 scan of
// silk_NLSF_del_dec_quant.
func nlsfDelDecMin4(v *[nlsfQuantDelDecStates]int32) (int32, int) {
	m, k := int32(math.MaxInt32), 0
	if v[0] < m {
		m, k = v[0], 0
	}
	if v[1] < m {
		m, k = v[1], 1
	}
	if v[2] < m {
		m, k = v[2], 2
	}
	if v[3] < m {
		m, k = v[3], 3
	}
	return m, k
}

// nlsfDelDecMax4 returns the largest of v above 0 and its first index, or
// (0, 0) when none is: the max_min_Q25 scan of silk_NLSF_del_dec_quant.
func nlsfDelDecMax4(v *[nlsfQuantDelDecStates]int32) (int32, int) {
	m, k := int32(0), 0
	if v[0] > m {
		m, k = v[0], 0
	}
	if v[1] > m {
		m, k = v[1], 1
	}
	if v[2] > m {
		m, k = v[2], 2
	}
	if v[3] > m {
		m, k = v[3], 3
	}
	return m, k
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

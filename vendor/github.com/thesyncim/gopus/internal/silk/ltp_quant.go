package silk

const (
	ltpQuantScaleQ17     = float32(131072.0) // 2^17
	ltpQuantSum1Q15      = int32(32801)      // round(1.001 * 2^15)
	ltpGainSafetyQ7      = int32(51)         // round(0.4 * 2^7)
	maxSumLogGainQ7Const = int32(5333)       // round((250/6) * 2^7)
	maxInt32             = int32(0x7fffffff)
)

func corrMatrixFLP(x []float32, subfrLen, order int, out []float32) {
	if subfrLen <= 0 || order <= 0 {
		for i := range out {
			out[i] = 0
		}
		return
	}

	// ptr1 points to &x[order-1] (start of column 0 of X)
	ptr1Idx := order - 1

	// BCE hints: x is accessed from index 0 to ptr1Idx+subfrLen-1
	_ = x[ptr1Idx+subfrLen-1]
	_ = out[order*order-1]

	// Match libopus silk/float/corrMatrix_FLP.c: silk_corrMatrix_FLP uses
	// a C double rolling accumulator, then stores each matrix cell as silk_float.
	energy := energyF32Libopus(x[ptr1Idx:ptr1Idx+subfrLen], subfrLen)
	out[0] = float32(energy)

	for j := 1; j < order; j++ {
		// Calculate X[:,j]'*X[:,j]
		term1 := x[ptr1Idx-j]
		term2 := x[ptr1Idx+subfrLen-j]
		// corrMatrix_FLP.c keeps the first product in the float expression but
		// rounds the subtracted product first. This lets the target compiler use
		// its native FMA policy for the first product, matching the paired C
		// object (FMADD after a rounded negative product on arm64).
		delta := term1*term1 - noFMA32(term2, term2)
		energy += silkCReal(delta)
		out[j*order+j] = float32(energy)
	}

	ptr2Idx := order - 2 // First sample of column 1 of X
	for lag := 1; lag < order; lag++ {
		// Calculate X[:,0]'*X[:,lag]
		xPtr1 := x[ptr1Idx:]
		xPtr2 := x[ptr2Idx:]
		inner := innerProductFLP(xPtr1, xPtr2, subfrLen)
		innerF32 := float32(inner)
		out[lag*order] = innerF32
		out[lag] = innerF32

		// Calculate X[:,j]'*X[:,j+lag]
		for j := 1; j < order-lag; j++ {
			term1 := x[ptr1Idx-j]
			term2 := x[ptr2Idx-j]
			term3 := x[ptr1Idx+subfrLen-j]
			term4 := x[ptr2Idx+subfrLen-j]
			// Preserve corrMatrix_FLP.c's float expression order for the rolling
			// update: C leaves the first product contractible and materializes the
			// trailing product before subtraction.
			delta := term1*term2 - noFMA32(term3, term4)
			inner += silkCReal(delta)
			innerF32 = float32(inner)
			out[(lag+j)*order+j] = innerF32
			out[j*order+(lag+j)] = innerF32
		}
		ptr2Idx--
	}
}

func corrVectorFLP(x, y []float32, subfrLen, order int, out []float32) {
	if subfrLen <= 0 || order <= 0 {
		for i := range out {
			out[i] = 0
		}
		return
	}

	// BCE hints
	_ = out[order-1]
	_ = y[subfrLen-1]
	_ = x[order-1+subfrLen-1]

	ptr1Idx := order - 1
	for lag := range order {
		xSlice := x[ptr1Idx:]
		out[lag] = float32(innerProductFLP(xSlice, y, subfrLen))
		ptr1Idx--
	}
}

func findLTPFLP(XX, xX []float32, residual []float32, resStart int, lag []int32, subfrLen, nbSubfr int) {
	xxIdx := 0
	xXIdx := 0
	rPtrStart := resStart
	for k := range nbSubfr {
		if k >= len(lag) {
			break
		}
		lagVal := int(lag[k])
		lagPtrStart := rPtrStart - (lagVal + ltpOrderConst/2)
		if lagPtrStart < 0 || rPtrStart < 0 || rPtrStart+subfrLen+ltpOrderConst > len(residual) || lagPtrStart+subfrLen+ltpOrderConst > len(residual) {
			for i := range ltpOrderConst * ltpOrderConst {
				XX[xxIdx+i] = 0
			}
			for i := range ltpOrderConst {
				xX[xXIdx+i] = 0
			}
			rPtrStart += subfrLen
			xxIdx += ltpOrderConst * ltpOrderConst
			xXIdx += ltpOrderConst
			continue
		}

		lagPtr := residual[lagPtrStart:]
		rPtr := residual[rPtrStart:]
		corrMatrixFLP(lagPtr, subfrLen, ltpOrderConst, XX[xxIdx:xxIdx+ltpOrderConst*ltpOrderConst])
		corrVectorFLP(lagPtr, rPtr, subfrLen, ltpOrderConst, xX[xXIdx:xXIdx+ltpOrderConst])

		// Match libopus: xx = (silk_float)silk_energy_FLP(r_ptr, subfr_length + LTP_ORDER)
		// The energy is computed in double but cast to silk_float (float32).
		xxF32 := float32(energyF32Libopus(rPtr, subfrLen+ltpOrderConst))
		diag0F32 := XX[xxIdx]
		diagLastF32 := XX[xxIdx+ltpOrderConst*ltpOrderConst-1]
		// Match libopus: temp = 1.0f / silk_max(xx, LTP_CORR_INV_MAX * 0.5f * (XX_ptr[0] + XX_ptr[24]) + 1.0f)
		// All float32 arithmetic.
		denomF32 := float32(ltpCorrInvMax)*0.5*(diag0F32+diagLastF32) + 1.0
		if xxF32 > denomF32 {
			denomF32 = xxF32
		}
		tempF32 := 1.0 / denomF32
		// Match libopus: silk_scale_vector_FLP(XX_ptr, temp, LTP_ORDER * LTP_ORDER)
		// silk_scale_vector_FLP multiplies silk_float by silk_float.
		// Use sub-slices with BCE hints for better bounds check elimination.
		{
			xxSub := XX[xxIdx : xxIdx+25]
			_ = xxSub[24] // BCE hint
			for i := range 25 {
				xxSub[i] *= tempF32
			}
		}
		{
			xXSub := xX[xXIdx : xXIdx+5]
			xXSub[0] *= tempF32
			xXSub[1] *= tempF32
			xXSub[2] *= tempF32
			xXSub[3] *= tempF32
			xXSub[4] *= tempF32
		}

		rPtrStart += subfrLen
		xxIdx += ltpOrderConst * ltpOrderConst
		xXIdx += ltpOrderConst
	}
}

func silkVQWMatEC(ind *int8, resNrgQ15 *int32, rateDistQ8 *int32, gainQ7 *int32, XX_Q17 []int32, xX_Q17 []int32, cb_Q7 []int8, cb_gain_Q7 []uint8, cl_Q5 []uint8, subfrLen int, maxGainQ7 int32, L int) {
	xx := (*[ltpOrderConst * ltpOrderConst]int32)(XX_Q17)
	var neg_xX_Q24 [ltpOrderConst]int32
	for i := range ltpOrderConst {
		neg_xX_Q24[i] = -silkLSHIFT(xX_Q17[i], 7)
	}

	bestRateDistQ8 := maxInt32
	bestResNrgQ15 := maxInt32
	bestInd := 0
	bestGainQ7 := *gainQ7

	cbGains := cb_gain_Q7[:L]
	cl := cl_Q5[:len(cbGains)]
	for k, g := range cbGains {
		cbRow := (*[ltpOrderConst]int8)(cb_Q7[k*ltpOrderConst : (k+1)*ltpOrderConst])
		gainTmpQ7 := int32(g)
		penalty := silkLSHIFT(silkMax32(gainTmpQ7-maxGainQ7, 0), 11)

		sum1_Q15 := ltpQuantSum1Q15

		sum2_Q24 := silkMLA(neg_xX_Q24[0], xx[1], int32(cbRow[1]))
		sum2_Q24 = silkMLA(sum2_Q24, xx[2], int32(cbRow[2]))
		sum2_Q24 = silkMLA(sum2_Q24, xx[3], int32(cbRow[3]))
		sum2_Q24 = silkMLA(sum2_Q24, xx[4], int32(cbRow[4]))
		sum2_Q24 = silkLSHIFT(sum2_Q24, 1)
		sum2_Q24 = silkMLA(sum2_Q24, xx[0], int32(cbRow[0]))
		sum1_Q15 = silkSMLAWB(sum1_Q15, sum2_Q24, int32(cbRow[0]))

		sum2_Q24 = silkMLA(neg_xX_Q24[1], xx[7], int32(cbRow[2]))
		sum2_Q24 = silkMLA(sum2_Q24, xx[8], int32(cbRow[3]))
		sum2_Q24 = silkMLA(sum2_Q24, xx[9], int32(cbRow[4]))
		sum2_Q24 = silkLSHIFT(sum2_Q24, 1)
		sum2_Q24 = silkMLA(sum2_Q24, xx[6], int32(cbRow[1]))
		sum1_Q15 = silkSMLAWB(sum1_Q15, sum2_Q24, int32(cbRow[1]))

		sum2_Q24 = silkMLA(neg_xX_Q24[2], xx[13], int32(cbRow[3]))
		sum2_Q24 = silkMLA(sum2_Q24, xx[14], int32(cbRow[4]))
		sum2_Q24 = silkLSHIFT(sum2_Q24, 1)
		sum2_Q24 = silkMLA(sum2_Q24, xx[12], int32(cbRow[2]))
		sum1_Q15 = silkSMLAWB(sum1_Q15, sum2_Q24, int32(cbRow[2]))

		sum2_Q24 = silkMLA(neg_xX_Q24[3], xx[19], int32(cbRow[4]))
		sum2_Q24 = silkLSHIFT(sum2_Q24, 1)
		sum2_Q24 = silkMLA(sum2_Q24, xx[18], int32(cbRow[3]))
		sum1_Q15 = silkSMLAWB(sum1_Q15, sum2_Q24, int32(cbRow[3]))

		sum2_Q24 = silkLSHIFT(neg_xX_Q24[4], 1)
		sum2_Q24 = silkMLA(sum2_Q24, xx[24], int32(cbRow[4]))
		sum1_Q15 = silkSMLAWB(sum1_Q15, sum2_Q24, int32(cbRow[4]))

		if sum1_Q15 >= 0 {
			bitsResQ8 := silkSMULBB(int32(subfrLen), silkLin2Log(sum1_Q15+penalty)-(15<<7))
			bitsTotQ8 := silkADD_LSHIFT32(bitsResQ8, int32(cl[k]), 2)
			if bitsTotQ8 <= bestRateDistQ8 {
				bestRateDistQ8 = bitsTotQ8
				bestResNrgQ15 = sum1_Q15 + penalty
				bestInd = k
				bestGainQ7 = gainTmpQ7
			}
		}
	}
	*rateDistQ8 = bestRateDistQ8
	*resNrgQ15 = bestResNrgQ15
	*ind = int8(bestInd)
	*gainQ7 = bestGainQ7
}

func silkQuantLTPGains(B_Q14 []int16, cbkIndex []int8, periodicityIndex *int8, sumLogGainQ7 *int32, predGainQ7 *int32, XX_Q17 []int32, xX_Q17 []int32, subfrLen, nbSubfr int) {
	minRateDistQ7 := maxInt32
	bestSumLogGainQ7 := int32(0)
	var tempIdx [maxNbSubfr]int8
	gainSafetyQ7 := ltpGainSafetyQ7
	maxSumLogGainQ7 := maxSumLogGainQ7Const
	var resNrgQ15 int32

	for k := range 3 {
		clPtr := silk_LTP_gain_BITS_Q5_ptrs[k]
		cbkPtr := silk_LTP_vq_ptrs_Q7[k]
		cbkGainPtr := silk_LTP_vq_gain_ptrs_Q7[k]
		cbkSize := int(silk_LTP_vq_sizes[k])

		XXPtr := XX_Q17
		xXPtr := xX_Q17
		resNrgQ15 = int32(0)
		rateDistQ7 := int32(0)
		sumLogGainTmpQ7 := *sumLogGainQ7

		for j := range nbSubfr {
			maxGainQ7 := silkLog2Lin((maxSumLogGainQ7-sumLogGainTmpQ7)+(7<<7)) - gainSafetyQ7

			var resNrgSubQ15, rateDistSubQ7, gainQ7 int32
			var idx int8
			silkVQWMatEC(&idx, &resNrgSubQ15, &rateDistSubQ7, &gainQ7, XXPtr, xXPtr, cbkPtr, cbkGainPtr, clPtr, subfrLen, maxGainQ7, cbkSize)
			tempIdx[j] = idx
			resNrgQ15 = silkAddPosSat32(resNrgQ15, resNrgSubQ15)
			rateDistQ7 = silkAddPosSat32(rateDistQ7, rateDistSubQ7)
			sumLogGainTmpQ7 = silkMax32(0, sumLogGainTmpQ7+silkLin2Log(gainSafetyQ7+gainQ7)-(7<<7))

			XXPtr = XXPtr[ltpOrderConst*ltpOrderConst:]
			xXPtr = xXPtr[ltpOrderConst:]
		}

		if rateDistQ7 <= minRateDistQ7 {
			minRateDistQ7 = rateDistQ7
			*periodicityIndex = int8(k)
			copy(cbkIndex, tempIdx[:nbSubfr])
			bestSumLogGainQ7 = sumLogGainTmpQ7
		}
	}

	cbkPtr := silk_LTP_vq_ptrs_Q7[*periodicityIndex]
	for j := range nbSubfr {
		base := int(cbkIndex[j]) * ltpOrderConst
		for k := range ltpOrderConst {
			B_Q14[j*ltpOrderConst+k] = int16(cbkPtr[base+k]) << 7
		}
	}

	if nbSubfr == 2 {
		*predGainQ7 = int32(silkSMULBB(-3, silkLin2Log(silkRSHIFT(resNrgQ15, 1))-(15<<7)))
	} else {
		*predGainQ7 = int32(silkSMULBB(-3, silkLin2Log(silkRSHIFT(resNrgQ15, 2))-(15<<7)))
	}
	*sumLogGainQ7 = bestSumLogGainQ7
}

func (e *Encoder) analyzeLTPQuantized(residual []float32, resStart int, pitchLags []int32, numSubframes, subframeSamples int) (LTPCoeffsArray, [maxNbSubfr]int8, int, int32) {
	var ltpCoeffs LTPCoeffsArray
	var cbkIndex [maxNbSubfr]int8
	perIndex := 0
	predGainQ7 := int32(0)

	if numSubframes <= 0 || len(pitchLags) == 0 || len(residual) == 0 {
		return ltpCoeffs, cbkIndex, perIndex, predGainQ7
	}

	if resStart < 0 {
		resStart = 0
	}
	if resStart >= len(residual) {
		resStart = 0
	}

	var XX [maxNbSubfr * ltpOrderConst * ltpOrderConst]float32
	var xX [maxNbSubfr * ltpOrderConst]float32
	findLTPFLP(XX[:], xX[:], residual, resStart, pitchLags, subframeSamples, numSubframes)

	var XXQ17 [maxNbSubfr * ltpOrderConst * ltpOrderConst]int32
	var xXQ17 [maxNbSubfr * ltpOrderConst]int32
	xxLen := numSubframes * ltpOrderConst * ltpOrderConst
	xXLen := numSubframes * ltpOrderConst
	// Match libopus wrappers_FLP.c: XX_Q17[i] = (opus_int32)silk_float2int(XX[i] * 131072.0f)
	// The multiplication XX[i] * 131072.0f is in float32 precision (silk_float * float literal).
	// silk_float2int uses lrintf (round to nearest, ties to even).
	for i := range xxLen {
		XXQ17[i] = float32ToInt32RoundEven(XX[i] * ltpQuantScaleQ17)
	}
	for i := range xXLen {
		xXQ17[i] = float32ToInt32RoundEven(xX[i] * ltpQuantScaleQ17)
	}

	var bQ14 [maxNbSubfr * ltpOrderConst]int16
	sumLogGainQ7 := e.sumLogGainQ7
	per := int8(0)
	silkQuantLTPGains(bQ14[:], cbkIndex[:], &per, &sumLogGainQ7, &predGainQ7, XXQ17[:], xXQ17[:], subframeSamples, numSubframes)
	e.sumLogGainQ7 = sumLogGainQ7
	perIndex = int(per)

	for sf := range numSubframes {
		for tap := range ltpOrderConst {
			ltpCoeffs[sf][tap] = int8(bQ14[sf*ltpOrderConst+tap] >> 7)
		}
	}

	return ltpCoeffs, cbkIndex, perIndex, predGainQ7
}

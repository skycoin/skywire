package silk

// silkNLSFUnpack unpacks, for a given first-stage NLSF codebook index, the
// per-coefficient entropy-table selectors (ecIx) and backward predictor
// coefficients (predQ8) used by the second-stage residual decode. Mirrors
// libopus silk/NLSF_unpack.c silk_NLSF_unpack.
func silkNLSFUnpack(ecIx []int16, predQ8 []uint8, cb *nlsfCB, cb1Index int) {
	order := int(cb.order)
	ecSelPtr := cb.ecSel[cb1Index*order/2:]
	for i := 0; i < order; i += 2 {
		entry := ecSelPtr[0]
		ecSelPtr = ecSelPtr[1:]
		ecIx[i] = int16(silkSMULBB(int32(entry>>1&7), 2*nlsfQuantMaxAmplitude+1))
		predQ8[i] = cb.predQ8[i+int(entry&1)*(order-1)]
		ecIx[i+1] = int16(silkSMULBB(int32(entry>>5&7), 2*nlsfQuantMaxAmplitude+1))
		predQ8[i+1] = cb.predQ8[i+int((entry>>4)&1)*(order-1)+1]
	}
}

// silkNLSFResidualDequant dequantizes the second-stage NLSF residual indices
// into Q10 residuals, running the backward predictor from high to low index and
// applying the quantization step size and level adjustment. Mirrors libopus
// silk/NLSF_residual_dequant.c silk_NLSF_residual_dequant.
func silkNLSFResidualDequant(xQ10 []int16, indices []int8, predQ8 []uint8, quantStepSizeQ16 int16, order int) {
	var outQ10 int32
	for i := order - 1; i >= 0; i-- {
		predQ10 := silkRSHIFT(silkSMULBB(outQ10, int32(predQ8[i])), 8)
		outQ10 = int32(indices[i]) << 10
		if outQ10 > 0 {
			outQ10 -= nlsfQuantLevelAdjQ10
		} else if outQ10 < 0 {
			outQ10 += nlsfQuantLevelAdjQ10
		}
		outQ10 = silkSMLAWB(predQ10, outQ10, int32(quantStepSizeQ16))
		xQ10[i] = int16(outQ10)
	}
}

// silkNLSFDecode reconstructs the normalized line-spectral-frequency vector
// (Q15) from its codebook indices: it unpacks the predictor/entropy selectors,
// dequantizes the residual, adds the first-stage codebook vector weighted by the
// inverse codebook weights, and stabilizes the result to a valid ordered set.
// Mirrors libopus silk/NLSF_decode.c silk_NLSF_decode.
func silkNLSFDecode(nlsfQ15 []int16, indices []int8, cb *nlsfCB) {
	var ecIx [maxLPCOrder]int16
	var predQ8 [maxLPCOrder]uint8
	var resQ10 [maxLPCOrder]int16

	silkNLSFDecodeInto(nlsfQ15, indices, cb, ecIx[:], predQ8[:], resQ10[:])
}

// silkNLSFDecodeInto decodes NLSF values using caller-provided scratch buffers.
// This avoids allocations in hot paths.
func silkNLSFDecodeInto(nlsfQ15 []int16, indices []int8, cb *nlsfCB, ecIx []int16, predQ8 []uint8, resQ10 []int16) {
	if cb == nil {
		return
	}
	order := int(cb.order)
	if len(indices) < order+1 || len(nlsfQ15) < order {
		return
	}

	if len(ecIx) < order {
		return
	}
	if len(predQ8) < order {
		return
	}
	if len(resQ10) < order {
		return
	}

	ecIx = ecIx[:order]
	predQ8 = predQ8[:order]
	resQ10 = resQ10[:order]

	silkNLSFUnpack(ecIx, predQ8, cb, int(indices[0]))
	silkNLSFResidualDequant(resQ10, indices[1:], predQ8, cb.quantStepSizeQ16, order)

	baseIdx := int(indices[0]) * order
	cbBase := cb.cb1NLSFQ8[baseIdx:]
	cbWght := cb.cb1WghtQ9[baseIdx:]
	for i := range order {
		resQ10Val := int32(resQ10[i])
		wght := int32(cbWght[i])
		if wght == 0 {
			wght = 1
		}
		val := min(max(silkADD_LSHIFT32(int32(resQ10Val<<14)/wght, int32(cbBase[i]), 7), 0), 32767)
		nlsfQ15[i] = int16(val)
	}

	silkNLSFStabilize(nlsfQ15[:order], cb.deltaMinQ15, order)
}

// silkNLSFStabilize enforces the minimum spacing (deltaMin) between adjacent
// NLSF coefficients and the [0, 1) range, first by iteratively widening the
// tightest gap and, if that does not converge within the loop budget, by a
// sorting fallback. This guarantees a stable LPC filter. Mirrors libopus
// silk/NLSF_stabilize.c silk_NLSF_stabilize.
func silkNLSFStabilize(nlsfQ15 []int16, deltaMinQ15 []int16, order int) {
	const maxLoops = 20
	for range maxLoops {
		minDiff := int32(nlsfQ15[0]) - int32(deltaMinQ15[0])
		idx := 0
		for i := 1; i <= order-1; i++ {
			diff := int32(nlsfQ15[i]) - (int32(nlsfQ15[i-1]) + int32(deltaMinQ15[i]))
			if diff < minDiff {
				minDiff = diff
				idx = i
			}
		}
		diff := int32(1<<15) - (int32(nlsfQ15[order-1]) + int32(deltaMinQ15[order]))
		if diff < minDiff {
			minDiff = diff
			idx = order
		}
		if minDiff >= 0 {
			return
		}
		if idx == 0 {
			nlsfQ15[0] = deltaMinQ15[0]
		} else if idx == order {
			nlsfQ15[order-1] = int16((1 << 15) - int32(deltaMinQ15[order]))
		} else {
			minCenter := int32(0)
			for k := 0; k < idx; k++ {
				minCenter += int32(deltaMinQ15[k])
			}
			minCenter += int32(deltaMinQ15[idx]) >> 1

			maxCenter := int32(1 << 15)
			for k := order; k > idx; k-- {
				maxCenter -= int32(deltaMinQ15[k])
			}
			maxCenter -= int32(deltaMinQ15[idx]) >> 1

			sum := int32(nlsfQ15[idx-1]) + int32(nlsfQ15[idx])
			center := silkRSHIFT_ROUND(sum, 1)
			center = silkLimit32(center, minCenter, maxCenter)
			nlsfQ15[idx-1] = int16(center - (int32(deltaMinQ15[idx]) >> 1))
			nlsfQ15[idx] = int16(int32(nlsfQ15[idx-1]) + int32(deltaMinQ15[idx]))
		}
	}

	silkInsertionSortInt16(nlsfQ15, order)
	if nlsfQ15[0] < deltaMinQ15[0] {
		nlsfQ15[0] = deltaMinQ15[0]
	}
	for i := 1; i < order; i++ {
		// Match libopus silk_ADD_SAT16: saturated int16 addition prevents
		// wrap-around when nlsfQ15[i-1] + deltaMinQ15[i] exceeds 32767.
		minVal := silkSAT16(int32(nlsfQ15[i-1]) + int32(deltaMinQ15[i]))
		if nlsfQ15[i] < minVal {
			nlsfQ15[i] = minVal
		}
	}
	lastMax := int16((1 << 15) - int32(deltaMinQ15[order]))
	if nlsfQ15[order-1] > lastMax {
		nlsfQ15[order-1] = lastMax
	}
	for i := order - 2; i >= 0; i-- {
		maxVal := int16(int32(nlsfQ15[i+1]) - int32(deltaMinQ15[i+1]))
		if nlsfQ15[i] > maxVal {
			nlsfQ15[i] = maxVal
		}
	}
}

// silkInsertionSortInt16 sorts the first n elements ascending in place. Used by
// the NLSF stabilization fallback. Mirrors the values-only variant of libopus
// silk/insertion_sort.c silk_insertion_sort_increasing.
func silkInsertionSortInt16(a []int16, n int) {
	for i := 1; i < n; i++ {
		key := a[i]
		j := i - 1
		for j >= 0 && a[j] > key {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = key
	}
}

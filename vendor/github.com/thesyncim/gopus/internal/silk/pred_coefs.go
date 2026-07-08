package silk

import "math"

func computeMinInvGain(predGainQ7 int32, codingQuality float32, firstFrame bool) float32 {
	if firstFrame {
		return float32(1.0 / maxPredictionPowerGainAfterReset)
	}

	// Match libopus find_pred_coefs_FLP.c precision:
	// minInvGain = (silk_float)pow( 2, LTPredCodGain / 3 ) / MAX_PREDICTION_POWER_GAIN;
	// minInvGain /= 0.25f + 0.75f * coding_quality;
	predGainF32 := float32(predGainQ7) / 128.0
	// LTPredCodGain / 3 is float32 in C (float / int promotes to float).
	powArg := predGainF32 / 3.0
	// pow(2, double) returns double, then (silk_float) casts to float.
	minInvGain := exp2F32(powArg) / float32(maxPredictionPowerGain)
	// float / float in C.
	minInvGain /= float32(0.25) + float32(0.75)*codingQuality

	if minInvGain < float32(1.0/maxPredictionPowerGain) {
		minInvGain = float32(1.0 / maxPredictionPowerGain)
	}
	if minInvGain > 1.0 {
		minInvGain = 1.0
	}
	return minInvGain
}

func (e *Encoder) buildLTPResidual(pitchBuf []float32, frameStart int, gains []float32, pitchLags []int32, ltpCoeffs LTPCoeffsArray, numSubframes, subframeSamples int, signalType int) []float32 {
	preLen := int(e.lpcOrder)
	outLen := numSubframes * (subframeSamples + preLen)
	ltpRes := ensureFloat32Slice(&e.scratchLtpResF32, outLen)

	for i := range ltpRes {
		ltpRes[i] = 0
	}

	// Match libopus silk_LTP_analysis_filter_FLP: operate entirely in float.
	// The input buffer is already int16-quantized (from quantizePCMToInt16),
	// scaled to [-1,1]. We scale to int16 range without redundant quantization.
	scale := float32(silkSampleScale)
	pitchBufLen := len(pitchBuf)

	for k := range numSubframes {
		outBase := k * (subframeSamples + preLen)
		xStart := frameStart - preLen + k*subframeSamples
		invGain := float32(1.0)
		if k < len(gains) && gains[k] > 0 {
			invGain = 1.0 / gains[k]
		}
		loopLen := subframeSamples + preLen
		if signalType == typeVoiced && k < len(pitchLags) {
			pitchLag := int(pitchLags[k])
			// Pre-compute LTP coefficients (invariant across samples in this subframe).
			var b0, b1, b2, b3, b4 float32
			b0 = float32(ltpCoeffs[k][0]) / 128.0
			b1 = float32(ltpCoeffs[k][1]) / 128.0
			b2 = float32(ltpCoeffs[k][2]) / 128.0
			b3 = float32(ltpCoeffs[k][3]) / 128.0
			b4 = float32(ltpCoeffs[k][4]) / 128.0

			// Compute bounds for the fast path (no per-sample bounds checks).
			// xIdx ranges from xStart to xStart+loopLen-1.
			// lagBase ranges from xStart-pitchLag to xStart+loopLen-1-pitchLag.
			// lagIdx ranges from lagBase-(ltpOrderConst/2) to lagBase+(ltpOrderConst/2).
			minAccess := xStart
			lagBaseMin := xStart - pitchLag
			lagMinAccess := lagBaseMin - ltpOrderConst/2
			if lagMinAccess < minAccess {
				minAccess = lagMinAccess
			}
			maxAccess := xStart + loopLen - 1
			lagBaseMax := maxAccess - pitchLag
			lagMaxAccess := lagBaseMax + ltpOrderConst/2
			if lagMaxAccess > maxAccess {
				maxAccess = lagMaxAccess
			}

			if minAccess >= 0 && maxAccess < pitchBufLen {
				// Fast path: all accesses are in bounds, no per-sample checks needed.
				_ = pitchBuf[maxAccess] // BCE hint
				_ = ltpRes[outBase+loopLen-1]
				for i := range loopLen {
					xIdx := xStart + i
					x := pitchBuf[xIdx] * scale
					lagBase := xIdx - pitchLag
					// Match libopus operation order:
					//   LTP_res[i] = x[i];
					//   for j: LTP_res[i] -= B[j] * x_lag[...];
					//   LTP_res[i] *= inv_gain;
					res := x
					res -= b0 * pitchBuf[lagBase+ltpOrderConst/2] * scale
					res -= b1 * pitchBuf[lagBase+ltpOrderConst/2-1] * scale
					res -= b2 * pitchBuf[lagBase+ltpOrderConst/2-2] * scale
					res -= b3 * pitchBuf[lagBase+ltpOrderConst/2-3] * scale
					res -= b4 * pitchBuf[lagBase+ltpOrderConst/2-4] * scale
					ltpRes[outBase+i] = res * invGain
				}
			} else {
				// Slow path: need per-sample bounds checking.
				for i := range loopLen {
					xIdx := xStart + i
					var x float32
					if xIdx >= 0 && xIdx < pitchBufLen {
						x = pitchBuf[xIdx] * scale
					}
					lagBase := xIdx - pitchLag
					res := x
					for j := range ltpOrderConst {
						lagIdx := lagBase + (ltpOrderConst/2 - j)
						bj := float32(ltpCoeffs[k][j]) / 128.0
						if lagIdx >= 0 && lagIdx < pitchBufLen {
							res -= bj * pitchBuf[lagIdx] * scale
						}
					}
					ltpRes[outBase+i] = res * invGain
				}
			}
		} else {
			// Unvoiced: simple scaling without LTP prediction.
			// Check if all accesses are in bounds for fast path.
			xEnd := xStart + loopLen - 1
			if xStart >= 0 && xEnd < pitchBufLen {
				_ = pitchBuf[xEnd] // BCE hint
				_ = ltpRes[outBase+loopLen-1]
				for i := range loopLen {
					ltpRes[outBase+i] = pitchBuf[xStart+i] * scale * invGain
				}
			} else {
				for i := range loopLen {
					xIdx := xStart + i
					if xIdx >= 0 && xIdx < pitchBufLen {
						ltpRes[outBase+i] = pitchBuf[xIdx] * scale * invGain
					}
				}
			}
		}
	}

	return ltpRes
}

func (e *Encoder) computeLPCAndNLSFWithInterp(ltpRes []float32, numSubframes, subframeSamples int, minInvGainVal float32) ([]int16, []int16, int) {
	order := int(e.lpcOrder)
	lpcQ12 := ensureInt16Slice(&e.scratchLpcQ12, order)
	lsfQ15 := ensureInt16Slice(&e.scratchLSFQ15, order)

	if order <= 0 {
		return lpcQ12, lsfQ15, 4
	}

	subfrLen := subframeSamples + order
	totalLen := numSubframes * subfrLen
	if totalLen <= 0 || totalLen > len(ltpRes) {
		for i := range order {
			lpcQ12[i] = 0
			lsfQ15[i] = int16((i + 1) * 32767 / (order + 1))
		}
		return lpcQ12, lsfQ15, 4
	}

	aFull, resNrg := e.burgModifiedFLPZeroAllocF32(ltpRes[:totalLen], minInvGainVal, subfrLen, numSubframes, order)
	resNrg32 := float32(resNrg)
	fullTotalEnergy := e.lastTotalEnergy
	fullInvGain := e.lastInvGain
	fullNumSamples := e.lastNumSamples

	for i := range order {
		a32 := float32(aFull[i])
		lpcQ12[i] = int16(float32ToInt32RoundEven(a32 * 4096.0))
	}

	lpcQ16 := ensureInt32Slice(&e.scratchLPCQ16, order)
	for i := range order {
		a32 := float32(aFull[i])
		lpcQ16[i] = float32ToInt32RoundEven(a32 * 65536.0)
	}
	silkA2NLSFInto(lsfQ15, lpcQ16, order, e.scratchA2nlsfP[:], e.scratchA2nlsfQ[:])

	interpIdx := 4
	useInterp := e.complexity >= 4 && !e.firstFrameAfterResetActive() && numSubframes == maxNbSubfr
	if useInterp {
		halfOffset := (maxNbSubfr / 2) * subfrLen
		if halfOffset+subfrLen*(maxNbSubfr/2) <= totalLen {
			aLast, resNrgLast := e.burgModifiedFLPZeroAllocF32(ltpRes[halfOffset:], minInvGainVal, subfrLen, maxNbSubfr/2, order)
			lsfLast := ensureInt16Slice(&e.scratchNLSFTempQ15, order)
			for i := range order {
				a32 := float32(aLast[i])
				lpcQ16[i] = float32ToInt32RoundEven(a32 * 65536.0)
			}
			silkA2NLSFInto(lsfLast, lpcQ16, order, e.scratchA2nlsfP[:], e.scratchA2nlsfQ[:])

			// Restore full-frame energy stats for gain processing.
			e.lastTotalEnergy = fullTotalEnergy
			e.lastInvGain = fullInvGain
			e.lastNumSamples = fullNumSamples

			resNrg32 -= resNrgLast
			resNrg2nd := float32(math.MaxFloat32)
			analyzeLen := 2 * subfrLen
			if analyzeLen <= totalLen {
				var interpNLSF [maxLPCOrder]int16
				var lpcTmpQ12 [maxLPCOrder]int16
				lpcTmpF32 := ensureFloat32Slice(&e.scratchPredCoefF32A, order)
				lpcRes := ensureFloat32Slice(&e.scratchLpcResF32, analyzeLen)

				for k := 3; k >= 0; k-- {
					interpolateNLSF(interpNLSF[:order], e.prevLSFQ15, lsfLast, k, order)
					// silk_NLSF2A_FLP calls silk_NLSF2A fixed-point
					if !silkNLSF2A(lpcTmpQ12[:order], interpNLSF[:order], order) {
						fallback := lsfToLPCDirect(interpNLSF[:order])
						copy(lpcTmpQ12[:order], fallback[:order])
					}
					for i := range order {
						lpcTmpF32[i] = float32(lpcTmpQ12[i]) / 4096.0
					}
					lpcAnalysisFilterF32(lpcRes, lpcTmpF32, ltpRes[:analyzeLen], analyzeLen, order)

					// Match libopus find_LPC_FLP.c exactly:
					// res_nrg_interp = (silk_float)( energy(seg0) + energy(seg1) );
					// Sum in double precision, cast once to float32.
					resNrgInterp := float32(
						energyF32Libopus(lpcRes[order:], subframeSamples) +
							energyF32Libopus(lpcRes[order+subfrLen:], subframeSamples),
					)

					if resNrgInterp < resNrg32 {
						resNrg32 = resNrgInterp
						interpIdx = k
					} else if resNrgInterp > resNrg2nd {
						break
					}
					resNrg2nd = resNrgInterp
				}
			}

			if interpIdx < 4 {
				copy(lsfQ15, lsfLast)
			}
		}
	}

	return lpcQ12, lsfQ15, interpIdx
}

func (e *Encoder) computeResidualEnergies(ltpRes []float32, predCoefQ12 []int16, interpIdx int, gains []float32, numSubframes, subframeSamples int) []float32 {
	order := int(e.lpcOrder)
	subfrLen := subframeSamples + order
	resNrg := ensureFloat32Slice(&e.scratchResNrg, numSubframes)
	for i := range resNrg {
		resNrg[i] = 0
	}

	if numSubframes == 0 || subfrLen <= order {
		return resNrg
	}

	coefToF32 := func(dst []float32, src []int16) {
		for i := range order {
			dst[i] = float32(src[i]) / 4096.0
		}
	}

	coef0 := ensureFloat32Slice(&e.scratchPredCoefF32A, order)
	coef1 := ensureFloat32Slice(&e.scratchPredCoefF32B, order)
	coefToF32(coef1, predCoefQ12[maxLPCOrder:maxLPCOrder+order])
	if interpIdx < 4 {
		coefToF32(coef0, predCoefQ12[:order])
	} else {
		copy(coef0, coef1)
	}

	coeffs := coef0
	if interpIdx >= 4 {
		coeffs = coef1
	}
	subframesInFirstHalf := min(numSubframes, 2)
	length := min(subframesInFirstHalf*subfrLen, len(ltpRes))
	if length > 0 {
		lpcRes := ensureFloat32Slice(&e.scratchLpcResF32, length)
		lpcAnalysisFilterF32(lpcRes, coeffs, ltpRes[:length], length, order)
		for k := 0; k < subframesInFirstHalf; k++ {
			start := order + k*subfrLen
			end := min(start+subframeSamples, len(lpcRes))
			energy := energyF32Libopus(lpcRes[start:end], end-start)
			gainSq := float32(1.0)
			if k < len(gains) {
				// Match libopus precision: gains[] are silk_float, so gain^2
				// is computed in float before promotion for energy multiply.
				g := gains[k]
				gainSq = g * g
			}
			energy *= silkCReal(gainSq)
			// Match libopus residual_energy_FLP output type (silk_float).
			resNrg[k] = float32(energy)
		}
	}

	if numSubframes == 4 {
		offset := 2 * subfrLen
		length := 2 * subfrLen
		if offset+length > len(ltpRes) {
			length = len(ltpRes) - offset
		}
		if length > 0 {
			lpcRes := ensureFloat32Slice(&e.scratchLpcResF32, length)
			lpcAnalysisFilterF32(lpcRes, coef1, ltpRes[offset:offset+length], length, order)
			for k := range 2 {
				start := order + k*subfrLen
				end := min(start+subframeSamples, len(lpcRes))
				energy := energyF32Libopus(lpcRes[start:end], end-start)
				idx := k + 2
				gainSq := float32(1.0)
				if idx < len(gains) {
					g := gains[idx]
					gainSq = g * g
				}
				energy *= silkCReal(gainSq)
				// Match libopus residual_energy_FLP output type (silk_float).
				resNrg[idx] = float32(energy)
			}
		}
	}

	return resNrg
}

func applyGainProcessing(gains []float32, resNrg []float32, predGainQ7 int32, snrDBQ7 int, signalType int, inputTiltQ15 int, subframeSamples int) int {
	quantOffsetType := 0
	if signalType == typeVoiced {
		predGainDB := float32(predGainQ7) / 128.0
		inputTilt := float32(inputTiltQ15) / 32768.0
		if predGainDB+inputTilt > 1.0 {
			quantOffsetType = 0
		} else {
			quantOffsetType = 1
		}

		// Match libopus process_gains_FLP.c sigmoid path for voiced gain reduction.
		// libopus: s = 1.0f - 0.5f * silk_sigmoid( 0.25f * ( LTPredCodGain - 12.0f ) )
		// silk_sigmoid(x) = (silk_float)(1.0 / (1.0 + exp(-x)))
		// Step 1: arg = 0.25f * (LTPredCodGain - 12.0f) — float32 arithmetic
		// Step 2: sigmoid = (float)(1.0 / (1.0 + exp((double)(-arg)))) — double internally, cast to float
		// Step 3: s = 1.0f - 0.5f * sigmoid — float32 arithmetic
		arg := float32(0.25) * (predGainDB - float32(12.0))
		sigmoid := 1.0 / (1.0 + expF32(-arg))
		s := float32(1.0) - float32(0.5)*sigmoid
		for k := range gains {
			gains[k] *= s
		}
	}

	snrDB := float32(snrDBQ7) / 128.0
	// Match libopus: InvMaxSqrVal = (silk_float)(pow(2.0f, 0.33f * (21.0f - SNR_dB_Q7 * (1/128.0f))) / subfr_length)
	// pow arg is float32, pow returns double, division by subfr_length is in double, then cast to float32.
	powArg := float32(0.33) * (float32(21.0) - snrDB)
	invMaxSqrVal := exp2DivIntF32(powArg, subframeSamples)

	for k := range gains {
		energy := gains[k] * gains[k]
		if k < len(resNrg) {
			energy += resNrg[k] * invMaxSqrVal
		}
		g := sqrt32(energy)
		if g > 32767.0 {
			g = 32767.0
		}
		gains[k] = g
	}

	return quantOffsetType
}

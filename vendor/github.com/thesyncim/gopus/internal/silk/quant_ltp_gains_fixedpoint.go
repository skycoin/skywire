//go:build gopus_fixed_point

package silk

// This file ports two libopus FIXED_POINT SILK kernels used as prerequisites by
// silk_find_pred_coefs_FIX:
//
//   1. silk_quant_LTP_gains (silk/quant_LTP_gains.c) - the entropy-constrained
//      LTP-gain vector quantizer. It searches the three silk_LTP_gain_* codebooks
//      (selected by periodicity_index), running silk_VQ_WMat_EC per subframe, and
//      produces the quantized LTP coefficients (LTPCoef_Q14), the periodicity and
//      per-subframe codebook indices, the cumulative max-prediction-gain
//      sum_log_gain_Q7 and the LTP prediction gain in dB (Q7).
//
//   2. silk_LTP_scale_ctrl_FIX (silk/fixed/LTP_scale_ctrl_FIX.c) - the
//      LTP-state-scaling-index decision. The C operates on the encoder state and
//      control structs; the pure-function form here takes exactly the fields it
//      reads and returns the chosen LTPscaleIndex and the corresponding
//      LTP_scale_Q14.
//
// The inner silk_VQ_WMat_EC (silk/VQ_WMat_EC.c) is ported as silkVQWMatECFixed.
// All arithmetic mirrors the C bit-for-bit.

// silkVQWMatECFixed is the bit-exact Go port of silk_VQ_WMat_EC_c
// (silk/VQ_WMat_EC.c): an entropy-constrained matrix-weighted VQ hard-coded to
// 5-element vectors for a single input data vector.
//
// rateDistQ8 receives the best total bitrate (Q8, despite the caller naming it
// rate_dist_Q7), resNrgQ15 the best residual energy, gainQ7 the codebook gain of
// the winner and ind the index of the best codebook vector.
func silkVQWMatECFixed(
	ind *int8,
	resNrgQ15 *int32,
	rateDistQ8 *int32,
	gainQ7 *int32,
	XXQ17 []int32,
	xXQ17 []int32,
	cbQ7 []int8,
	cbGainQ7 []uint8,
	clQ5 []uint8,
	subfrLen int,
	maxGainQ7 int32,
	L int,
) {
	const order = ltpOrder // LTP_ORDER == 5

	// Negate and convert to new Q domain.
	var negXXQ24 [order]int32
	negXXQ24[0] = -silkLSHIFT(xXQ17[0], 7)
	negXXQ24[1] = -silkLSHIFT(xXQ17[1], 7)
	negXXQ24[2] = -silkLSHIFT(xXQ17[2], 7)
	negXXQ24[3] = -silkLSHIFT(xXQ17[3], 7)
	negXXQ24[4] = -silkLSHIFT(xXQ17[4], 7)

	// Loop over codebook.
	*rateDistQ8 = silk_int32_MAX
	*resNrgQ15 = silk_int32_MAX
	// If things go really bad, at least *ind is set to something safe.
	*ind = 0

	for k := 0; k < L; k++ {
		cbRow := cbQ7[k*order:]
		gainTmpQ7 := int32(cbGainQ7[k])

		// Weighted rate.
		// Quantization error: 1 - 2 * xX * cb + cb' * XX * cb.
		sum1Q15 := int32(silkFixConst(1.001, 15))

		// Penalty for too large gain.
		penalty := silkLSHIFT(silkMax32(gainTmpQ7-maxGainQ7, 0), 11)

		// first row of XX_Q17.
		sum2Q24 := silkMLA(negXXQ24[0], XXQ17[1], int32(cbRow[1]))
		sum2Q24 = silkMLA(sum2Q24, XXQ17[2], int32(cbRow[2]))
		sum2Q24 = silkMLA(sum2Q24, XXQ17[3], int32(cbRow[3]))
		sum2Q24 = silkMLA(sum2Q24, XXQ17[4], int32(cbRow[4]))
		sum2Q24 = silkLSHIFT(sum2Q24, 1)
		sum2Q24 = silkMLA(sum2Q24, XXQ17[0], int32(cbRow[0]))
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int32(cbRow[0]))

		// second row of XX_Q17.
		sum2Q24 = silkMLA(negXXQ24[1], XXQ17[7], int32(cbRow[2]))
		sum2Q24 = silkMLA(sum2Q24, XXQ17[8], int32(cbRow[3]))
		sum2Q24 = silkMLA(sum2Q24, XXQ17[9], int32(cbRow[4]))
		sum2Q24 = silkLSHIFT(sum2Q24, 1)
		sum2Q24 = silkMLA(sum2Q24, XXQ17[6], int32(cbRow[1]))
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int32(cbRow[1]))

		// third row of XX_Q17.
		sum2Q24 = silkMLA(negXXQ24[2], XXQ17[13], int32(cbRow[3]))
		sum2Q24 = silkMLA(sum2Q24, XXQ17[14], int32(cbRow[4]))
		sum2Q24 = silkLSHIFT(sum2Q24, 1)
		sum2Q24 = silkMLA(sum2Q24, XXQ17[12], int32(cbRow[2]))
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int32(cbRow[2]))

		// fourth row of XX_Q17.
		sum2Q24 = silkMLA(negXXQ24[3], XXQ17[19], int32(cbRow[4]))
		sum2Q24 = silkLSHIFT(sum2Q24, 1)
		sum2Q24 = silkMLA(sum2Q24, XXQ17[18], int32(cbRow[3]))
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int32(cbRow[3]))

		// last row of XX_Q17.
		sum2Q24 = silkLSHIFT(negXXQ24[4], 1)
		sum2Q24 = silkMLA(sum2Q24, XXQ17[24], int32(cbRow[4]))
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int32(cbRow[4]))

		// find best.
		if sum1Q15 >= 0 {
			// Translate residual energy to bits using high-rate assumption
			// (6 dB ==> 1 bit/sample).
			bitsResQ8 := silkSMULBB(int32(subfrLen), silkLin2Log(sum1Q15+penalty)-(15<<7))
			// The codelength component is reduced by half ("3-1"); this slightly
			// improves quality.
			bitsTotQ8 := silkADD_LSHIFT32(bitsResQ8, int32(clQ5[k]), 3-1)
			if bitsTotQ8 <= *rateDistQ8 {
				*rateDistQ8 = bitsTotQ8
				*resNrgQ15 = sum1Q15 + penalty
				*ind = int8(k)
				*gainQ7 = gainTmpQ7
			}
		}
	}
}

// silkQuantLTPGainsFixed is the bit-exact Go port of silk_quant_LTP_gains
// (silk/quant_LTP_gains.c).
//
// BQ14 (length nbSubfr*ltpOrder) receives the quantized LTP gains, cbkIndex
// (length nbSubfr) the per-subframe codebook indices, periodicityIndex the
// chosen codebook, sumLogGainQ7 (in/out) the cumulative max prediction gain and
// predGainQ7 the LTP prediction gain in dB (Q7). XXQ17 (length
// nbSubfr*ltpOrder*ltpOrder) and xXQ17 (length nbSubfr*ltpOrder) hold the Q17
// correlation matrices/vectors.
func silkQuantLTPGainsFixed(
	BQ14 []int16,
	cbkIndex []int8,
	periodicityIndex *int8,
	sumLogGainQ7 *int32,
	predGainQ7 *int32,
	XXQ17 []int32,
	xXQ17 []int32,
	subfrLen int,
	nbSubfr int,
) {
	const order = ltpOrder // LTP_ORDER == 5

	var tempIdx [maxNbSubfr]int8

	// iterate over different codebooks with different rates/distortions, and
	// choose best.
	minRateDistQ7 := silk_int32_MAX
	bestSumLogGainQ7 := int32(0)
	var resNrgQ15 int32

	for k := 0; k < 3; k++ {
		// Safety margin for pitch gain control, to take into account factors
		// such as state rescaling/rewhitening.
		gainSafety := int32(silkFixConst(0.4, 7))

		clPtrQ5 := silk_LTP_gain_BITS_Q5_ptrs[k]
		cbkPtrQ7 := silk_LTP_vq_ptrs_Q7[k]
		cbkGainPtrQ7 := silk_LTP_vq_gain_ptrs_Q7[k]
		cbkSize := int(silk_LTP_vq_sizes[k])

		// Set up pointers to first subframe.
		XXQ17Ptr := XXQ17
		xXQ17Ptr := xXQ17

		resNrgQ15 = 0
		rateDistQ7 := int32(0)
		sumLogGainTmpQ7 := *sumLogGainQ7

		for j := 0; j < nbSubfr; j++ {
			// SILK_FIX_CONST( MAX_SUM_LOG_GAIN_DB / 6.0, 7 ); MAX_SUM_LOG_GAIN_DB = 250.0.
			maxGainQ7 := silkLog2Lin((int32(silkFixConst(250.0/6.0, 7))-sumLogGainTmpQ7)+
				int32(silkFixConst(7, 7))) - gainSafety

			var resNrgQ15Subfr, rateDistQ7Subfr, gainQ7 int32
			var idx int8
			silkVQWMatECFixed(
				&idx,             // index of best codebook vector
				&resNrgQ15Subfr,  // residual energy
				&rateDistQ7Subfr, // best weighted quantization error + mu * rate
				&gainQ7,          // sum of absolute LTP coefficients
				XXQ17Ptr,         // correlation matrix
				xXQ17Ptr,         // correlation vector
				cbkPtrQ7,         // codebook
				cbkGainPtrQ7,     // codebook effective gains
				clPtrQ5,          // code length for each codebook vector
				subfrLen,         // number of samples per subframe
				maxGainQ7,        // maximum sum of absolute LTP coefficients
				cbkSize,          // number of vectors in codebook
			)

			resNrgQ15 = silkAddPosSat32(resNrgQ15, resNrgQ15Subfr)
			rateDistQ7 = silkAddPosSat32(rateDistQ7, rateDistQ7Subfr)
			sumLogGainTmpQ7 = silkMax32(0, sumLogGainTmpQ7+
				silkLin2Log(gainSafety+gainQ7)-int32(silkFixConst(7, 7)))

			tempIdx[j] = idx

			XXQ17Ptr = XXQ17Ptr[order*order:]
			xXQ17Ptr = xXQ17Ptr[order:]
		}

		if rateDistQ7 <= minRateDistQ7 {
			minRateDistQ7 = rateDistQ7
			*periodicityIndex = int8(k)
			copy(cbkIndex[:nbSubfr], tempIdx[:nbSubfr])
			bestSumLogGainQ7 = sumLogGainTmpQ7
		}
	}

	cbkPtrQ7 := silk_LTP_vq_ptrs_Q7[*periodicityIndex]
	for j := 0; j < nbSubfr; j++ {
		for k := 0; k < order; k++ {
			BQ14[j*order+k] = int16(silkLSHIFT(int32(cbkPtrQ7[int(cbkIndex[j])*order+k]), 7))
		}
	}

	if nbSubfr == 2 {
		resNrgQ15 = silkRSHIFT(resNrgQ15, 1)
	} else {
		resNrgQ15 = silkRSHIFT(resNrgQ15, 2)
	}

	*sumLogGainQ7 = bestSumLogGainQ7
	*predGainQ7 = silkSMULBB(-3, silkLin2Log(resNrgQ15)-(15<<7))
}

// silkLTPScaleCtrlFixed is the bit-exact Go port of silk_LTP_scale_ctrl_FIX
// (silk/fixed/LTP_scale_ctrl_FIX.c), in pure-function form. It returns the LTP
// scale index and the corresponding LTP_scale_Q14 looked up from
// silk_LTPScales_table_Q14.
//
// Inputs mirror the encoder-state/control fields the C reads: ltpredCodGainQ7
// (psEncCtrl->LTPredCodGain_Q7), packetLossPerc (sCmn.PacketLoss_perc),
// nFramesPerPacket (sCmn.nFramesPerPacket), lbrrFlag (sCmn.LBRR_flag),
// snrDBQ7 (sCmn.SNR_dB_Q7) and condCoding.
func silkLTPScaleCtrlFixed(
	ltpredCodGainQ7 int32,
	packetLossPerc int32,
	nFramesPerPacket int32,
	lbrrFlag int32,
	snrDBQ7 int32,
	condCoding int32,
) (ltpScaleIndex int32, ltpScaleQ14 int32) {
	if condCoding == codeIndependently {
		// Only scale if first frame in packet.
		roundLoss := packetLossPerc * nFramesPerPacket
		if lbrrFlag != 0 {
			// LBRR reduces the effective loss. In practice it does not square
			// the loss because losses aren't independent, but that still seems
			// to work best. We also never go below 2%.
			roundLoss = 2 + silkSMULBB(roundLoss, roundLoss)/100
		}
		if silkSMULBB(ltpredCodGainQ7, roundLoss) > silkLog2Lin(128*7+2900-snrDBQ7) {
			ltpScaleIndex = 1
		}
		if silkSMULBB(ltpredCodGainQ7, roundLoss) > silkLog2Lin(128*7+3900-snrDBQ7) {
			ltpScaleIndex++
		}
	} else {
		// Default is minimum scaling.
		ltpScaleIndex = 0
	}
	ltpScaleQ14 = int32(silk_LTPScales_table_Q14[ltpScaleIndex])
	return ltpScaleIndex, ltpScaleQ14
}

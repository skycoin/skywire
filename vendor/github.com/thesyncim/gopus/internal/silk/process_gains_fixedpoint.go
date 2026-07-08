//go:build gopus_fixed_point

package silk

// This file ports the libopus FIXED_POINT SILK gain-processing kernel from
// silk/fixed/process_gains_FIX.c: silk_process_gains_FIX. It transforms the
// noise-shaping gains (Gains_Q16) into quantized gain indices, computing the
// noise-floor limit, the soft limit on the residual-energy-to-squared-gain
// ratio, the gain quantization (silk_gains_quant), the voiced quantizer
// offset selection, and the Lambda_Q10 used by the residual quantizer.
//
// The state/control structs are flattened into silkProcessGainsParams so the
// kernel can be exercised in isolation against its own C oracle.

// Tuning constants from silk/tuning_parameters.h, expressed as their
// SILK_FIX_CONST(x, q) integer values exactly as the C compiler computes
// them (int)(x * (1<<q) + 0.5 for x>=0, -0.5 for x<0).
const (
	// SILK_FIX_CONST(C, Q) = (opus_int32)(C*(1<<Q) + 0.5), truncating toward 0.
	// LAMBDA_OFFSET = 1.2f, Q10: (int)(1.2*1024 + 0.5) = 1229
	pgLambdaOffsetQ10 = int32(1229)
	// LAMBDA_DELAYED_DECISIONS = -0.05f, Q10: (int)(-0.05*1024 + 0.5) = -50
	pgLambdaDelayedDecisionsQ10 = int32(-50)
	// LAMBDA_SPEECH_ACT = -0.2f, Q18: (int)(-0.2*262144 + 0.5) = -52428
	pgLambdaSpeechActQ18 = int32(-52428)
	// LAMBDA_INPUT_QUALITY = -0.1f, Q12: (int)(-0.1*4096 + 0.5) = -409
	pgLambdaInputQualityQ12 = int32(-409)
	// LAMBDA_CODING_QUALITY = -0.2f, Q12: (int)(-0.2*4096 + 0.5) = -818
	pgLambdaCodingQualityQ12 = int32(-818)
	// LAMBDA_QUANT_OFFSET = 0.8f, Q16: (int)(0.8*65536 + 0.5) = 52429
	pgLambdaQuantOffsetQ16 = int32(52429)

	// SILK_FIX_CONST(12.0, 7) = 1536
	pgConst12Q7 = int32(1536)
	// SILK_FIX_CONST(21 + 16/0.33, 7) = (int)((21+48.4848...)*128 + 0.5) = 8894
	pgConst21Q7 = int32(8894)
	// SILK_FIX_CONST(0.33, 16) = (int)(0.33*65536 + 0.5) = 21627
	pgConst033Q16 = int32(21627)
	// SILK_FIX_CONST(1.0, 7) = 128
	pgConst1Q7 = int32(128)

	// pgInt16MAX from silk/typedef.h
	pgInt16MAX = int32(0x7FFF)
)

// silkSigmQ15 is the bit-exact Go port of silk_sigm_Q15 (silk/sigm_Q15.c):
// a piecewise-linear sigmoid lookup with Q15 output and Q5 input.
func silkSigmQ15(inQ5 int32) int32 {
	sigmLUTslopeQ10 := [6]int32{237, 153, 73, 30, 12, 7}
	sigmLUTposQ15 := [6]int32{16384, 23955, 28861, 31213, 32178, 32548}
	sigmLUTnegQ15 := [6]int32{16384, 8812, 3906, 1554, 589, 219}

	if inQ5 < 0 {
		inQ5 = -inQ5
		if inQ5 >= 6*32 {
			return 0
		}
		ind := silkRSHIFT(inQ5, 5)
		return sigmLUTnegQ15[ind] - silkSMULBB(sigmLUTslopeQ10[ind], inQ5&0x1F)
	}
	if inQ5 >= 6*32 {
		return 32767
	}
	ind := silkRSHIFT(inQ5, 5)
	return sigmLUTposQ15[ind] + silkSMULBB(sigmLUTslopeQ10[ind], inQ5&0x1F)
}

// silkProcessGainsParams mirrors the silk_encoder_state_FIX /
// silk_encoder_control_FIX fields read by silk_process_gains_FIX.
type silkProcessGainsParams struct {
	// From sCmn (silk_encoder_state)
	signalType             int32
	nbSubfr                int
	subfrLength            int32
	snrDBQ7                int32
	inputTiltQ15           int32
	nStatesDelayedDecision int32
	speechActivityQ8       int32
	quantOffsetType        int32 // in/out: overwritten for voiced

	// From psEncCtrl (silk_encoder_control_FIX)
	ltpredCodGainQ7  int32
	inputQualityQ14  int32
	codingQualityQ14 int32
	gainsQ16         []int32 // in/out, len nbSubfr
	resNrg           []int32 // len nbSubfr
	resNrgQ          []int32 // len nbSubfr

	// Shape state / quantization carry
	lastGainIndex int8 // in/out (psShapeSt->LastGainIndex)

	condCoding int32
}

// silkProcessGainsResult holds the kernel outputs.
type silkProcessGainsResult struct {
	gainsIndices      []int8  // len nbSubfr
	gainsUnqQ16       []int32 // len nbSubfr
	lastGainIndexPrev int8
	lastGainIndex     int8 // updated psShapeSt->LastGainIndex
	quantOffsetType   int32
	lambdaQ10         int32
}

// silkProcessGainsFixed is the bit-exact Go port of silk_process_gains_FIX.
// It mutates p.gainsQ16 in place (matching the C which writes Gains_Q16) and
// returns the derived indices, unquantized gains and Lambda_Q10.
func silkProcessGainsFixed(sc *silkFixedEncodeScratch, p *silkProcessGainsParams) silkProcessGainsResult {
	nb := p.nbSubfr

	// Gain reduction when LTP coding gain is high.
	if p.signalType == typeVoiced {
		sQ16 := -silkSigmQ15(silkRSHIFT_ROUND(p.ltpredCodGainQ7-pgConst12Q7, 4))
		for k := 0; k < nb; k++ {
			p.gainsQ16[k] = silkSMLAWB(p.gainsQ16[k], p.gainsQ16[k], sQ16)
		}
	}

	// Limit the quantized signal.
	// InvMaxSqrVal = pow(2, 0.33*(21-SNR_dB)) / subfr_length;
	invMaxSqrValQ16 := silkDiv32_16(
		silkLog2Lin(silkSMULWB(pgConst21Q7-p.snrDBQ7, pgConst033Q16)),
		p.subfrLength)

	for k := 0; k < nb; k++ {
		// Soft limit on ratio residual energy and squared gains.
		resNrg := p.resNrg[k]
		resNrgPart := silkSMULWW(resNrg, invMaxSqrValQ16)
		if p.resNrgQ[k] > 0 {
			resNrgPart = silkRSHIFT_ROUND(resNrgPart, int(p.resNrgQ[k]))
		} else {
			if resNrgPart >= silkRSHIFT(silk_int32_MAX, int(-p.resNrgQ[k])) {
				resNrgPart = silk_int32_MAX
			} else {
				resNrgPart = silkLSHIFT(resNrgPart, int(-p.resNrgQ[k]))
			}
		}
		gain := p.gainsQ16[k]
		gainSquared := silkAddSat32(resNrgPart, silkSMMUL(gain, gain))
		if gainSquared < pgInt16MAX {
			// Recalculate with higher precision.
			gainSquared = silkSMLAWW(silkLSHIFT(resNrgPart, 16), gain, gain)
			gain = silkSqrtApproxPLC(gainSquared) // Q8
			if gain > silk_int32_MAX>>8 {
				gain = silk_int32_MAX >> 8
			}
			p.gainsQ16[k] = silkLShiftSAT32(gain, 8) // Q16
		} else {
			gain = silkSqrtApproxPLC(gainSquared) // Q0
			if gain > silk_int32_MAX>>16 {
				gain = silk_int32_MAX >> 16
			}
			p.gainsQ16[k] = silkLShiftSAT32(gain, 16) // Q16
		}
	}

	var res silkProcessGainsResult

	// Save unquantized gains and gain index.
	res.gainsUnqQ16 = ensureInt32Slice(&sc.pgGainsUnqQ16, nb)
	copy(res.gainsUnqQ16, p.gainsQ16[:nb])
	res.lastGainIndexPrev = p.lastGainIndex

	// Quantize gains.
	res.gainsIndices = ensureInt8Slice(&sc.pgGainsIndices, nb)
	res.lastGainIndex = silkGainsQuantInto(res.gainsIndices, p.gainsQ16,
		p.lastGainIndex, p.condCoding == codeConditionally, nb)

	// Quantizer offset for voiced signals.
	res.quantOffsetType = p.quantOffsetType
	if p.signalType == typeVoiced {
		if p.ltpredCodGainQ7+silkRSHIFT(p.inputTiltQ15, 8) > pgConst1Q7 {
			res.quantOffsetType = 0
		} else {
			res.quantOffsetType = 1
		}
	}

	// Quantizer boundary adjustment -> Lambda_Q10.
	quantOffsetQ10 := int32(silk_Quantization_Offsets_Q10[p.signalType>>1][res.quantOffsetType])
	res.lambdaQ10 = pgLambdaOffsetQ10 +
		silkSMULBB(pgLambdaDelayedDecisionsQ10, p.nStatesDelayedDecision) +
		silkSMULWB(pgLambdaSpeechActQ18, p.speechActivityQ8) +
		silkSMULWB(pgLambdaInputQualityQ12, p.inputQualityQ14) +
		silkSMULWB(pgLambdaCodingQualityQ12, p.codingQualityQ14) +
		silkSMULWB(pgLambdaQuantOffsetQ16, quantOffsetQ10)

	return res
}

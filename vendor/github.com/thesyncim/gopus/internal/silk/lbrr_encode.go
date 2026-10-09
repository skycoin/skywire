// Package silk implements LBRR (Low Bitrate Redundancy) encoding for FEC.
// LBRR provides forward error correction by including redundant data
// for the previous frame at a lower quality in the current packet.
//
// Reference: libopus silk/encode_frame_FLP.c silk_LBRR_encode_FLP
package silk

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// lbrrSpeechActivityThresholdQ8 is the minimum speech activity for LBRR.
// Frames with lower activity are not LBRR-encoded.
// Reference: libopus tuning_parameters.h LBRR_SPEECH_ACTIVITY_THRES = 0.3f
// SILK_FIX_CONST(0.3, 8) = (int32)(0.3 * 256 + 0.5) = 77
const lbrrSpeechActivityThresholdQ8 = 77

// lbrrEncode encodes LBRR (low bitrate redundancy) data for the current frame.
// This creates a lower-quality version of the frame that can be used for
// packet loss concealment by the decoder.
//
// LBRR is encoded with:
// - Same LPC/LSF coefficients as the main frame
// - Higher gains (to reduce bits used for excitation)
// - Same pitch parameters for voiced frames
// - Requantized excitation at lower quality
//
// Reference: libopus silk/float/encode_frame_FLP.c silk_LBRR_encode_FLP
func (e *Encoder) lbrrEncode(
	pcm []float32,
	frameIndices sideInfoIndices,
	lpcQ12 []int16,
	predCoefQ12 []int16,
	interpIdx int,
	pitchLags []int32,
	ltpCoeffs LTPCoeffsArray,
	ltpScaleIndex int,
	noiseParams *NoiseShapeParams,
	seed int,
	numSubframes, subframeSamples, frameSamples int,
	speechActivityQ8 int,
	currentLastGainIndex int8,
	condCoding int,
) {
	if !e.lbrrEnabled {
		return
	}

	// Only encode LBRR for frames with sufficient speech activity
	// Match libopus: speech_activity_Q8 > SILK_FIX_CONST(LBRR_SPEECH_ACTIVITY_THRES, 8)
	if speechActivityQ8 <= lbrrSpeechActivityThresholdQ8 {
		e.lbrrFlags[e.nFramesEncoded] = 0
		return
	}

	frameIdx := e.nFramesEncoded

	// Mark this frame for LBRR
	e.lbrrFlags[frameIdx] = 1
	e.lbrrIndices[frameIdx] = frameIndices
	e.lbrrFrameLength[frameIdx] = int32(frameSamples)
	e.lbrrNbSubfr[frameIdx] = int32(numSubframes)

	// For first LBRR frame or after non-LBRR, increase first gain
	if frameIdx == 0 || e.lbrrFlags[frameIdx-1] == 0 {
		// Save current frame's gain index for next LBRR frame.
		// Match libopus: LBRRprevLastGainIndex = sShape.LastGainIndex
		// which is already updated by silk_gains_quant for the current frame.
		e.lbrrPrevLastGainIdx = currentLastGainIndex

		// Increase gain by LBRR_GainIncreases steps
		gainIdx := int(e.lbrrIndices[frameIdx].GainsIndices[0])
		gainIdx += int(e.lbrrGainIncreases)
		if gainIdx > nLevelsQGain-1 {
			gainIdx = nLevelsQGain - 1
		}
		if gainIdx < 0 {
			gainIdx = 0
		}
		e.lbrrIndices[frameIdx].GainsIndices[0] = int8(gainIdx)
	}
	// Dequantize LBRR gains for NSQ using the primary frame's condCoding,
	// matching libopus silk_LBRR_encode_FLP (not the LBRR payload condCoding).
	lbrrGainsQ16 := ensureInt32Slice(&e.scratchLBRRGainsQ16, numSubframes)
	e.decodeLBRRGains(lbrrGainsQ16, condCoding, numSubframes)

	if frameSamples <= 0 {
		return
	}

	// LBRR quantizes from a copy of the primary NSQ state (sNSQ_LBRR in
	// silk_LBRR_encode_FLP); the scratch buffers are per call and shared.
	e.lbrrNSQState = *e.nsqState
	lbrrNSQ := &e.lbrrNSQState
	signalType := int(e.lbrrIndices[frameIdx].signalType)
	quantOffset := int(e.lbrrIndices[frameIdx].quantOffsetType)
	ltpScaleQ14 := int32(0)
	if signalType == typeVoiced {
		ltpScaleQ14 = int32(silk_LTPScales_table_Q14[ltpScaleIndex])
	}
	pulses, seedOut := e.computeNSQExcitation(pcm, lpcQ12, predCoefQ12, interpIdx, lbrrGainsQ16, pitchLags, ltpCoeffs, ltpScaleQ14, signalType, quantOffset, speechActivityQ8, noiseParams, seed, numSubframes, subframeSamples, frameSamples, lbrrNSQ)
	e.lbrrIndices[frameIdx].Seed = int8(seedOut)

	lbrrPulses := e.lbrrPulses[frameIdx]
	for i := 0; i < frameSamples && i < len(pulses); i++ {
		lbrrPulses[i] = pulses[i]
	}
}

// decodeLBRRGains dequantizes LBRR gain indices to Q16 gains.
// This ensures gains are in sync with what the decoder will compute.
func (e *Encoder) decodeLBRRGains(gainsQ16 []int32, condCoding int, nbSubfr int) {
	indices := &e.lbrrIndices[int(e.nFramesEncoded)]

	var gainsArr [maxNbSubfr]int32
	var indicesArr [maxNbSubfr]int8
	for i := range maxNbSubfr {
		indicesArr[i] = indices.GainsIndices[i]
	}
	prev := e.lbrrPrevLastGainIdx
	silkGainsDequant(&gainsArr, &indicesArr, &prev, condCoding == codeConditionally, nbSubfr)
	e.lbrrPrevLastGainIdx = prev

	for i := 0; i < nbSubfr && i < len(gainsQ16); i++ {
		gainsQ16[i] = gainsArr[i]
	}
}

// encodeLBRRIndices encodes the LBRR indices for a single frame.
func (e *Encoder) encodeLBRRIndices(re *rangecoding.Encoder, frameIdx, condCoding int, prevSignalType *int, prevLagIndex *int) {
	indices := &e.lbrrIndices[frameIdx]
	signalType := int(indices.signalType)
	quantOffset := int(indices.quantOffsetType)
	nbSubfr := int(e.lbrrNbSubfr[frameIdx])
	if nbSubfr <= 0 || nbSubfr > maxNbSubfr {
		nbSubfr = maxNbSubfr
	}

	// Encode signal type and quantizer offset (LBRR uses VAD table)
	typeOffset := min(max(2*signalType+quantOffset, 2), 5)
	re.EncodeICDF(typeOffset-2, silk_type_offset_VAD_iCDF, 8)

	// Encode gains
	if condCoding == codeConditionally {
		for i := 0; i < nbSubfr; i++ {
			re.EncodeICDF(int(indices.GainsIndices[i]), silk_delta_gain_iCDF, 8)
		}
	} else {
		gainIdx := min(max(int(indices.GainsIndices[0]), 0), nLevelsQGain-1)
		msb := gainIdx >> 3
		lsb := gainIdx & 7
		stype := signalType
		if stype < 0 || stype > 2 {
			stype = 0
		}
		re.EncodeICDF(msb, silk_gain_iCDF[stype], 8)
		re.EncodeICDF(lsb, silk_uniform8_iCDF, 8)
		for i := 1; i < nbSubfr; i++ {
			re.EncodeICDF(int(indices.GainsIndices[i]), silk_delta_gain_iCDF, 8)
		}
	}

	// Encode NLSFs
	var cb *nlsfCB
	if e.bandwidth == BandwidthWideband {
		cb = &silk_NLSF_CB_WB
	} else {
		cb = &silk_NLSF_CB_NB_MB
	}
	stypeBand := signalType >> 1
	order := int(cb.order)
	nVectors := int(cb.nVectors)
	cb1Offset := stypeBand * nVectors
	stage1Idx := max(int(indices.NLSFIndices[0]), 0)
	if stage1Idx >= nVectors {
		stage1Idx = nVectors - 1
	}
	re.EncodeICDF(stage1Idx, cb.cb1ICDF[cb1Offset:], 8)

	ecIx := ensureInt16Slice(&e.scratchEcIx, order)
	predQ8 := ensureUint8Slice(&e.scratchPredQ8, order)
	silkNLSFUnpack(ecIx, predQ8, cb, stage1Idx)
	for i := range order {
		idx := int(indices.NLSFIndices[i+1])
		if idx >= nlsfQuantMaxAmplitude {
			re.EncodeICDF(2*nlsfQuantMaxAmplitude, cb.ecICDF[int(ecIx[i]):], 8)
			re.EncodeICDF(idx-nlsfQuantMaxAmplitude, silk_NLSF_EXT_iCDF, 8)
		} else if idx <= -nlsfQuantMaxAmplitude {
			re.EncodeICDF(0, cb.ecICDF[int(ecIx[i]):], 8)
			re.EncodeICDF(-idx-nlsfQuantMaxAmplitude, silk_NLSF_EXT_iCDF, 8)
		} else {
			re.EncodeICDF(idx+nlsfQuantMaxAmplitude, cb.ecICDF[int(ecIx[i]):], 8)
		}
	}

	if nbSubfr == maxNbSubfr {
		interp := min(max(int(indices.NLSFInterpCoefQ2), 0), 4)
		re.EncodeICDF(interp, silk_NLSF_interpolation_factor_iCDF, 8)
	}

	if signalType == typeVoiced {
		fsKHz := GetBandwidthConfig(e.bandwidth).SampleRate / 1000
		_, contourICDF, lagLowICDF := pitchLagTables(fsKHz, nbSubfr)

		encodeAbsolute := true
		if condCoding == codeConditionally && prevSignalType != nil && *prevSignalType == typeVoiced {
			delta := int(indices.lagIndex) - *prevLagIndex
			if delta < -8 || delta > 11 {
				delta = 0
			} else {
				delta += 9
				encodeAbsolute = false
			}
			re.EncodeICDF(delta, silk_pitch_delta_iCDF, 8)
		}

		if encodeAbsolute {
			divisor := max(fsKHz/2, 1)
			lagIdx := int(indices.lagIndex)
			lagHigh := lagIdx / divisor
			lagLow := lagIdx - lagHigh*divisor
			if lagHigh > 31 {
				lagHigh = 31
			}
			if lagLow < 0 {
				lagLow = 0
			}
			if lagLow > len(lagLowICDF)-1 {
				lagLow = len(lagLowICDF) - 1
			}
			re.EncodeICDF(lagHigh, silk_pitch_lag_iCDF, 8)
			re.EncodeICDF(lagLow, lagLowICDF, 8)
		}

		if prevLagIndex != nil {
			*prevLagIndex = int(indices.lagIndex)
		}

		contourIdx := min(max(int(indices.contourIndex), 0), len(contourICDF)-1)
		re.EncodeICDF(contourIdx, contourICDF, 8)

		per := min(max(int(indices.PERIndex), 0), 2)
		re.EncodeICDF(per, silk_LTP_per_index_iCDF, 8)
		for k := 0; k < nbSubfr; k++ {
			idx := max(int(indices.LTPIndex[k]), 0)
			maxIdx := 8 << per
			if idx >= maxIdx {
				idx = maxIdx - 1
			}
			re.EncodeICDF(idx, silk_LTP_gain_iCDF_ptrs[per], 8)
		}

		if condCoding == codeIndependently {
			ltpScale := min(max(int(indices.LTPScaleIndex), 0), 2)
			re.EncodeICDF(ltpScale, silk_LTPscale_iCDF, 8)
		}
	}

	if prevSignalType != nil {
		*prevSignalType = signalType
	}

	seed := min(max(int(indices.Seed), 0), 3)
	re.EncodeICDF(seed, silk_uniform4_iCDF, 8)
}

// encodeLBRRPulses encodes the LBRR pulses for a single frame.
func (e *Encoder) encodeLBRRPulses(re *rangecoding.Encoder, frameIdx int) {
	pulses := e.lbrrPulses[frameIdx]
	signalType := int(e.lbrrIndices[frameIdx].signalType)
	quantOffset := int(e.lbrrIndices[frameIdx].quantOffsetType)

	frameLength := int(e.lbrrFrameLength[frameIdx])
	if frameLength <= 0 || frameLength > len(pulses) {
		frameLength = len(pulses)
	}

	// Use the standard pulse encoding on the active packet range encoder.
	prevRE := e.rangeEncoder
	e.rangeEncoder = re
	e.encodePulses(pulses[:frameLength], signalType, quantOffset)
	e.rangeEncoder = prevRE
}

// Note: silk_LBRR_flags_iCDF_ptr is defined in libopus_tables.go
// Note: silkLog2Lin is defined in libopus_log.go

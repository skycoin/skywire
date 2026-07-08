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

	// Deep-copy NSQ state so LBRR quantization cannot alias the primary path.
	lbrrNSQ := e.nsqState.Clone()
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

// encodeLBRRData encodes the LBRR flags and data at the start of the packet.
// This should be called at the beginning of the first frame encoding.
//
// Reference: libopus silk/enc_API.c lines 355-405
func (e *Encoder) encodeLBRRData(re *rangecoding.Encoder, nChannels int, includeHeader bool) {
	if e.nFramesEncoded != 0 {
		// LBRR is only encoded at the start of the packet
		return
	}

	if includeHeader {
		// Create space at start of payload for VAD and FEC flags
		// This is done by encoding a placeholder that will be patched later
		iCDF := []uint16{
			uint16(256 - (256 >> ((int(e.nFramesPerPacket) + 1) * nChannels))),
			0,
		}
		re.EncodeICDF16(0, iCDF, 8)
	}

	// Track LBRR bits: start measuring AFTER the VAD/FEC header reservation,
	// matching libopus enc_API.c: curr_nBitsUsedLBRR = ec_tell(psRangeEnc);
	lbrrBitsStart := re.Tell()

	// Encode LBRR flags
	lbrrSymbol := 0
	nFrames := int(e.nFramesPerPacket)
	for i := range nFrames {
		lbrrSymbol |= int(e.lbrrFlags[i]) << i
	}

	// Set the overall LBRR flag
	lbrrFlag := 0
	if lbrrSymbol > 0 {
		lbrrFlag = 1
	}
	e.lbrrFlag = int8(lbrrFlag)

	// If LBRR is present and there are multiple frames, encode the flags
	if lbrrFlag != 0 && nFrames > 1 {
		// Use silk_LBRR_flags_iCDF_ptr
		re.EncodeICDF(lbrrSymbol-1, silk_LBRR_flags_iCDF_ptr[nFrames-2], 8)
	}

	// Encode LBRR indices and pulses for each frame
	lbrrPrevSignalType := 0
	lbrrPrevLagIndex := 0
	for i := range nFrames {
		if e.lbrrFlags[i] == 0 {
			continue
		}

		condCoding := codeIndependently
		if i > 0 && e.lbrrFlags[i-1] != 0 {
			condCoding = codeConditionally
		}

		// Encode LBRR indices
		e.encodeLBRRIndices(re, i, condCoding, &lbrrPrevSignalType, &lbrrPrevLagIndex)

		// Encode LBRR pulses
		e.encodeLBRRPulses(re, i)
	}

	// Record the LBRR header bits emitted for this frame. libopus enc_API.c folds
	// these into the nBitsUsedLBRR exponential moving average inside the per-frame
	// rate-control loop (lines 406-425), with curr_nBitsUsedLBRR re-zeroed each
	// frame; only the frame that writes the packet's LBRR header has non-zero bits.
	// The EMA update + nBits subtraction runs in EncodeFrame so every frame of a
	// multi-frame packet applies it (frames after the first see curr=0 and reset
	// the EMA, matching libopus exactly).
	e.currNBitsUsedLBRR = int32(re.Tell() - lbrrBitsStart)

	// Clear LBRR flags after encoding (they apply to the previous packet)
	for i := range e.lbrrFlags {
		e.lbrrFlags[i] = 0
	}
}

// EncodeLBRRData encodes LBRR data with optional header placeholder.
// If includeHeader is false, the caller is responsible for reserving header bits.
func (e *Encoder) EncodeLBRRData(re *rangecoding.Encoder, nChannels int, includeHeader bool) {
	e.encodeLBRRData(re, nChannels, includeHeader)
}

// applyLBRRReservoirUpdate folds this frame's LBRR header bits (currNBitsUsedLBRR)
// into the nBitsUsedLBRR exponential moving average and consumes the count. This
// is the per-frame update libopus enc_API.c runs inside the rate-control loop
// (lines 418-424); the frame that writes the packet's LBRR header carries the raw
// bits, every later frame in the packet sees curr==0 and resets the EMA to zero.
// It must run exactly once per frame, before that frame's target-rate is derived.
func (e *Encoder) applyLBRRReservoirUpdate() {
	curr := e.currNBitsUsedLBRR
	if curr < 10 {
		e.nBitsUsedLBRR = 0
	} else if e.nBitsUsedLBRR < 10 {
		e.nBitsUsedLBRR = curr
	} else {
		e.nBitsUsedLBRR = (e.nBitsUsedLBRR + curr) / 2
	}
	e.currNBitsUsedLBRR = 0
}

// encodeLBRRFlagSymbol writes per-frame LBRR flags for one channel and returns the flag.
func encodeLBRRFlagSymbol(re *rangecoding.Encoder, enc *Encoder, nFrames int) int {
	lbrrSymbol := 0
	for i := range nFrames {
		lbrrSymbol |= int(enc.lbrrFlags[i]) << i
	}
	lbrrFlag := 0
	if lbrrSymbol > 0 {
		lbrrFlag = 1
	}
	enc.lbrrFlag = int8(lbrrFlag)
	if lbrrFlag != 0 && nFrames > 1 {
		re.EncodeICDF(lbrrSymbol-1, silk_LBRR_flags_iCDF_ptr[nFrames-2], 8)
	}
	return lbrrFlag
}

// encodeStereoLBRRPacket encodes stereo LBRR flags and payloads at packet start.
// Order matches skipStereoLBRRFrames / libopus enc_API.c.
func encodeStereoLBRRPacket(
	re *rangecoding.Encoder,
	midEnc, sideEnc *Encoder,
	nFrames int,
	stereo *stereoEncState,
) {
	if re == nil || midEnc == nil || sideEnc == nil || stereo == nil {
		return
	}

	lbrrBitsStart := re.Tell()
	encodeLBRRFlagSymbol(re, midEnc, nFrames)
	encodeLBRRFlagSymbol(re, sideEnc, nFrames)

	var midPrevSignalType, midPrevLagIndex int
	var sidePrevSignalType, sidePrevLagIndex int
	channels := []*Encoder{midEnc, sideEnc}
	for i := range nFrames {
		for ch, enc := range channels {
			if enc.lbrrFlags[i] == 0 {
				continue
			}
			if ch == 0 {
				EncodeStereoIndices(re, stereo.lbrrStereoIx[i])
				if sideEnc.lbrrFlags[i] == 0 {
					EncodeStereoMidOnly(re, int(stereo.lbrrMidOnly[i]))
				}
			}
			condCoding := codeIndependently
			if i > 0 && enc.lbrrFlags[i-1] != 0 {
				condCoding = codeConditionally
			}
			if ch == 0 {
				midEnc.encodeLBRRIndices(re, i, condCoding, &midPrevSignalType, &midPrevLagIndex)
				midEnc.encodeLBRRPulses(re, i)
			} else {
				sideEnc.encodeLBRRIndices(re, i, condCoding, &sidePrevSignalType, &sidePrevLagIndex)
				sideEnc.encodeLBRRPulses(re, i)
			}
		}
	}

	// Record the whole-section LBRR header bits for this packet; the EMA update +
	// nBits subtraction runs per-frame in the encode body (libopus enc_API.c),
	// where frames after the first see curr=0 and reset the EMA. The stereo target
	// rate keys off the mid encoder's nBitsUsedLBRR, so the raw count lives there.
	midEnc.currNBitsUsedLBRR = int32(re.Tell() - lbrrBitsStart)

	for i := range midEnc.lbrrFlags {
		midEnc.lbrrFlags[i] = 0
	}
	for i := range sideEnc.lbrrFlags {
		sideEnc.lbrrFlags[i] = 0
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
	typeOffset := max(2*signalType+quantOffset, 2)
	if typeOffset > 5 {
		typeOffset = 5
	}
	re.EncodeICDF(typeOffset-2, silk_type_offset_VAD_iCDF, 8)

	// Encode gains
	if condCoding == codeConditionally {
		for i := 0; i < nbSubfr; i++ {
			re.EncodeICDF(int(indices.GainsIndices[i]), silk_delta_gain_iCDF, 8)
		}
	} else {
		gainIdx := max(int(indices.GainsIndices[0]), 0)
		if gainIdx > nLevelsQGain-1 {
			gainIdx = nLevelsQGain - 1
		}
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
		interp := max(int(indices.NLSFInterpCoefQ2), 0)
		if interp > 4 {
			interp = 4
		}
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

		contourIdx := max(int(indices.contourIndex), 0)
		if contourIdx > len(contourICDF)-1 {
			contourIdx = len(contourICDF) - 1
		}
		re.EncodeICDF(contourIdx, contourICDF, 8)

		per := max(int(indices.PERIndex), 0)
		if per > 2 {
			per = 2
		}
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
			ltpScale := max(int(indices.LTPScaleIndex), 0)
			if ltpScale > 2 {
				ltpScale = 2
			}
			re.EncodeICDF(ltpScale, silk_LTPscale_iCDF, 8)
		}
	}

	if prevSignalType != nil {
		*prevSignalType = signalType
	}

	seed := max(int(indices.Seed), 0)
	if seed > 3 {
		seed = 3
	}
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

// hasLBRRData returns true if there is LBRR data to encode.
func (e *Encoder) hasLBRRData() bool {
	for i := 0; i < int(e.nFramesPerPacket); i++ {
		if e.lbrrFlags[i] != 0 {
			return true
		}
	}
	return false
}

// HasLBRRData reports whether there is pending LBRR data to encode.
func (e *Encoder) HasLBRRData() bool {
	return e.hasLBRRData()
}

// Note: silk_LBRR_flags_iCDF_ptr is defined in libopus_tables.go
// Note: silkLog2Lin is defined in libopus_log.go

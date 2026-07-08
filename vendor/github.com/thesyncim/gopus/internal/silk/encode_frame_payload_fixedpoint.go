//go:build gopus_fixed_point

package silk

import "github.com/thesyncim/gopus/internal/rangecoding"

// This file assembles the public FIXED_POINT SILK per-frame encoder
// (silk/fixed/encode_frame_FIX.c: silk_encode_frame_FIX) on top of the
// bit-exact analysis chain (silkEncodeFrameFIXAnalyze) and the shared integer
// range-coder kernels (silkEncodeIndices / encodePulses). It ports the parts of
// silk_encode_frame_FIX that the analysis driver does not cover:
//
//   - silk_LBRR_encode_FIX: the LBRR re-quantization at increased gain,
//   - the 6-iteration gain/Lambda rate-control loop (gainsID, found-lower /
//     found-upper search, gain-lock per subframe, damage-control fallback),
//   - the payload finalization (ec_tell -> nBytesOut, ec_prev* bookkeeping,
//     first_frame_after_reset clear).
//
// LP variable-cutoff filtering and the x_buf shift/insert are handled by the
// caller (the float EncodeFrame already does the equivalent); this driver
// operates on the prepared silkEncodeFrameFIXState (x_buf with the new frame in
// place) exactly as silk_encode_frame_FIX does after silk_LP_variable_cutoff.

// LBRR_SPEECH_ACTIVITY_THRES = 0.3f -> SILK_FIX_CONST(0.3, 8) = 77.
const lbrrSpeechActivityThresQ8 = 77

// silkEncodeFramePayloadFIXState extends the analysis state with the
// per-encoder bookkeeping fields silk_encode_frame_FIX reads/writes that are
// not part of the pure analysis chain.
type silkEncodeFramePayloadFIXState struct {
	silkEncodeFrameFIXState

	// Conditional-pitch coding carry (sCmn.ec_prev*).
	ecPrevLagIndex   int16
	ecPrevSignalType int32

	// LBRR control (sCmn.*).
	lbrrEnabled           bool
	lbrrGainIncreases     int32
	lbrrPrevLastGainIndex *int8 // sCmn.LBRRprevLastGainIndex (carried across frames)

	// nFramesEncoded indexes LBRR_flags / indices_LBRR / pulses_LBRR.
	nFramesEncoded int
	// lbrrPrevFrameHadLBRR is LBRR_flags[nFramesEncoded-1] (0 when first frame).
	lbrrPrevFrameHadLBRR bool

	// Range encoder managed by the caller (shared for hybrid / multi-frame).
	rangeEncoder *rangecoding.Encoder

	// maxBits / useCBR for the rate-control loop.
	maxBits int
	useCBR  bool

	// Bandwidth selects the entropy-coder ICDF tables (pitch contour / lag low
	// bits / NLSF codebook).
	bandwidth Bandwidth
}

// silkEncodeFramePayloadFIXResult carries the LBRR side-info / pulses captured
// for the current frame plus the encoded payload bookkeeping.
type silkEncodeFramePayloadFIXResult struct {
	lbrrFlag    int
	lbrrIndices sideInfoIndices
	lbrrPulses  []int8
	// nBytesOut is silk_RSHIFT(ec_tell + 7, 3) captured after the loop.
	nBytesOut int
	// vadFlag is the integer VAD decision (silk_encode_do_VAD_FIX result) for
	// this frame: 1 == VAD-active. It feeds the SILK VAD header bit and, for the
	// stereo side channel, the mid-only-flag coding gate.
	vadFlag int
}

// silkEncodeIndices is the bit-exact Go port of silk_encode_indices
// (silk/encode_indices.c). It encodes the side-information for one frame from
// the SideInfoIndices struct, using the shared integer range-coder kernels.
// The encodeLBRR flag selects the LBRR type table (always VAD) exactly as the C.
func (e *Encoder) silkEncodeIndices(
	re *rangecoding.Encoder,
	indices *sideInfoIndices,
	fsKHz, nbSubfr int,
	bandwidth Bandwidth,
	condCoding int,
	encodeLBRR bool,
) {
	signalType := int(indices.signalType)
	quantOffset := int(indices.quantOffsetType)

	// Encode signal type and quantizer offset.
	typeOffset := 2*signalType + quantOffset
	if encodeLBRR || typeOffset >= 2 {
		re.EncodeICDF(typeOffset-2, silk_type_offset_VAD_iCDF, 8)
	} else {
		re.EncodeICDF(typeOffset, silk_type_offset_no_VAD_iCDF, 8)
	}

	// Encode gains: first subframe.
	if condCoding == codeConditionally {
		re.EncodeICDF(int(indices.GainsIndices[0]), silk_delta_gain_iCDF, 8)
	} else {
		re.EncodeICDF(int(indices.GainsIndices[0])>>3, silk_gain_iCDF[signalType], 8)
		re.EncodeICDF(int(indices.GainsIndices[0])&7, silk_uniform8_iCDF, 8)
	}
	for i := 1; i < nbSubfr; i++ {
		re.EncodeICDF(int(indices.GainsIndices[i]), silk_delta_gain_iCDF, 8)
	}

	// Encode NLSFs.
	var cb *nlsfCB
	if bandwidth == BandwidthWideband {
		cb = &silk_NLSF_CB_WB
	} else {
		cb = &silk_NLSF_CB_NB_MB
	}
	nVectors := int(cb.nVectors)
	order := int(cb.order)
	cb1Offset := (signalType >> 1) * nVectors
	re.EncodeICDF(int(indices.NLSFIndices[0]), cb.cb1ICDF[cb1Offset:], 8)

	ecIx := ensureInt16Slice(&e.scratchEcIx, order)
	predQ8 := ensureUint8Slice(&e.scratchPredQ8, order)
	silkNLSFUnpack(ecIx, predQ8, cb, int(indices.NLSFIndices[0]))
	for i := 0; i < order; i++ {
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

	// Encode NLSF interpolation factor.
	if nbSubfr == maxNbSubfr {
		re.EncodeICDF(int(indices.NLSFInterpCoefQ2), silk_NLSF_interpolation_factor_iCDF, 8)
	}

	if signalType == typeVoiced {
		_, contourICDF, lagLowICDF := pitchLagTables(fsKHz, nbSubfr)

		// Encode pitch lags.
		encodeAbsolute := true
		if condCoding == codeConditionally && e.ecPrevSignalType == typeVoiced {
			delta := int(indices.lagIndex) - int(e.ecPrevLagIndex)
			if delta < -8 || delta > 11 {
				delta = 0
			} else {
				delta += 9
				encodeAbsolute = false
			}
			re.EncodeICDF(delta, silk_pitch_delta_iCDF, 8)
		}
		if encodeAbsolute {
			divisor := fsKHz >> 1
			pitchHigh := int(indices.lagIndex) / divisor
			pitchLow := int(indices.lagIndex) - pitchHigh*divisor
			re.EncodeICDF(pitchHigh, silk_pitch_lag_iCDF, 8)
			re.EncodeICDF(pitchLow, lagLowICDF, 8)
		}
		e.ecPrevLagIndex = indices.lagIndex

		// Contour index.
		re.EncodeICDF(int(indices.contourIndex), contourICDF, 8)

		// Encode LTP gains: PERIndex value.
		re.EncodeICDF(int(indices.PERIndex), silk_LTP_per_index_iCDF, 8)
		for k := 0; k < nbSubfr; k++ {
			re.EncodeICDF(int(indices.LTPIndex[k]), silk_LTP_gain_iCDF_ptrs[indices.PERIndex], 8)
		}

		// Encode LTP scaling.
		if condCoding == codeIndependently {
			re.EncodeICDF(int(indices.LTPScaleIndex), silk_LTPscale_iCDF, 8)
		}
	}

	e.ecPrevSignalType = int32(signalType)

	// Encode seed.
	re.EncodeICDF(int(indices.Seed), silk_uniform4_iCDF, 8)
}

// silkRunNSQFIX selects silk_NSQ vs silk_NSQ_del_dec exactly as
// silk_encode_frame_FIX (del-dec when nStatesDelayedDecision > 1 or warping >
// 0). It writes pulses and updates the indices seed for the del-dec path.
func silkRunNSQFIX(
	sc *silkFixedEncodeScratch,
	st *silkEncodeFrameFIXState,
	nsq *NSQState,
	indices *sideInfoIndices,
	ctrl *sEncCtrlFIX,
	x16 []int16,
	pulses []int8,
) {
	if st.nStatesDelayedDecision > 1 || st.warpingQ16 > 0 {
		seedOut := silkNSQDelDecFixed(
			sc,
			nsq,
			int(indices.Seed),
			int(indices.signalType),
			int(indices.quantOffsetType),
			int(indices.NLSFInterpCoefQ2),
			x16,
			pulses,
			ctrl.predCoefQ12[0],
			ctrl.ltpCoefQ14,
			ctrl.arQ13,
			ctrl.harmShapeGainQ14,
			ctrl.tiltQ14,
			ctrl.lfShpQ14,
			ctrl.gainsQ16,
			ctrl.pitchL,
			ctrl.lambdaQ10,
			ctrl.ltpScaleQ14,
			st.ltpMemLength,
			st.frameLength,
			st.subfrLength,
			st.nbSubfr,
			st.predictLPCOrder,
			st.shapingLPCOrder,
			st.warpingQ16,
			st.nStatesDelayedDecision,
		)
		indices.Seed = int8(seedOut)
	} else {
		silkNSQFixed(
			sc,
			nsq,
			int(indices.Seed),
			int(indices.signalType),
			int(indices.quantOffsetType),
			int(indices.NLSFInterpCoefQ2),
			x16,
			pulses,
			ctrl.predCoefQ12[0],
			ctrl.ltpCoefQ14,
			ctrl.arQ13,
			ctrl.harmShapeGainQ14,
			ctrl.tiltQ14,
			ctrl.lfShpQ14,
			ctrl.gainsQ16,
			ctrl.pitchL,
			ctrl.lambdaQ10,
			ctrl.ltpScaleQ14,
			st.ltpMemLength,
			st.frameLength,
			st.subfrLength,
			st.nbSubfr,
			st.predictLPCOrder,
			st.shapingLPCOrder,
		)
	}
}

// ctrlToIndices builds a SideInfoIndices snapshot from the analysis control
// struct and the current gain indices / seed.
func ctrlToIndices(ctrl *sEncCtrlFIX, gainIndices []int8, seed int8) sideInfoIndices {
	var idx sideInfoIndices
	idx.signalType = ctrl.signalType
	idx.quantOffsetType = ctrl.quantOffsetType
	idx.NLSFInterpCoefQ2 = ctrl.nlsfInterpCoefQ2
	idx.PERIndex = ctrl.perIndex
	idx.LTPScaleIndex = ctrl.ltpScaleIndex
	idx.Seed = seed
	idx.lagIndex = ctrl.lagIndex
	idx.contourIndex = ctrl.contourIndex
	for i := 0; i < len(ctrl.nlsfIndices) && i < len(idx.NLSFIndices); i++ {
		idx.NLSFIndices[i] = ctrl.nlsfIndices[i]
	}
	for i := 0; i < maxNbSubfr; i++ {
		idx.LTPIndex[i] = ctrl.ltpIndex[i]
	}
	for i := 0; i < len(gainIndices) && i < maxNbSubfr; i++ {
		idx.GainsIndices[i] = gainIndices[i]
	}
	return idx
}

// silkLBRREncodeFIX is the bit-exact Go port of silk_LBRR_encode_FIX
// (encode_frame_FIX.c): it re-quantizes the excitation at an increased gain to
// produce the redundant LBRR pulses, reusing the regular analysis control
// struct. It returns the LBRR indices and pulses for the current frame, and
// whether LBRR was produced.
func (e *Encoder) silkLBRREncodeFIX(
	sc *silkFixedEncodeScratch,
	ps *silkEncodeFramePayloadFIXState,
	ctrl *sEncCtrlFIX,
	x16 []int16,
	gainIndices []int8,
	seed int8,
) (sideInfoIndices, []int8, bool) {
	st := &ps.silkEncodeFrameFIXState
	if !ps.lbrrEnabled || st.speechActivityQ8 <= lbrrSpeechActivityThresQ8 {
		return sideInfoIndices{}, nil, false
	}

	// Copy NSQ state and indices from the regular encoding.
	var sNSQLBRR NSQState
	sNSQLBRR = st.nsq
	indicesLBRR := ctrlToIndices(ctrl, gainIndices, seed)

	// Save original gains.
	tempGainsQ16 := ensureInt32Slice(&sc.lbrrTempGainsQ16, st.nbSubfr)
	copy(tempGainsQ16, ctrl.gainsQ16[:st.nbSubfr])

	if ps.nFramesEncoded == 0 || !ps.lbrrPrevFrameHadLBRR {
		if ps.lbrrPrevLastGainIndex != nil {
			*ps.lbrrPrevLastGainIndex = st.lastGainIndex
		}
		g := int(indicesLBRR.GainsIndices[0]) + int(ps.lbrrGainIncreases)
		if g > nLevelsQGain-1 {
			g = nLevelsQGain - 1
		}
		indicesLBRR.GainsIndices[0] = int8(g)
	}

	// Dequantize to get gains in sync with the decoder.
	var gainsQ16 [maxNbSubfr]int32
	var gainsIdx [maxNbSubfr]int8
	copy(gainsIdx[:], indicesLBRR.GainsIndices[:])
	prevIdx := st.lastGainIndex
	if ps.lbrrPrevLastGainIndex != nil {
		prevIdx = *ps.lbrrPrevLastGainIndex
	}
	silkGainsDequant(&gainsQ16, &gainsIdx, &prevIdx, st.condCoding == codeConditionally, st.nbSubfr)
	if ps.lbrrPrevLastGainIndex != nil {
		*ps.lbrrPrevLastGainIndex = prevIdx
	}

	// NSQ with LBRR gains.
	lbrrPulses := ensureInt8Slice(&sc.lbrrPulses, st.frameLength)
	var lbrrCtrl sEncCtrlFIX = *ctrl
	lbrrCtrl.gainsQ16 = gainsQ16[:st.nbSubfr]
	silkRunNSQFIX(sc, st, &sNSQLBRR, &indicesLBRR, &lbrrCtrl, x16, lbrrPulses)

	// Original gains are restored implicitly (we operated on a copy).
	_ = tempGainsQ16
	return indicesLBRR, lbrrPulses, true
}

// silkEncodeFramePayloadFIX is the bit-exact Go port of the control / rate loop
// of silk_encode_frame_FIX. It runs the analysis chain, LBRR, the gain/Lambda
// iteration and the payload finalization, emitting the side-info + excitation
// to ps.rangeEncoder. It returns the LBRR capture and the byte count.
func (e *Encoder) silkEncodeFramePayloadFIX(ps *silkEncodeFramePayloadFIXState) silkEncodeFramePayloadFIXResult {
	st := &ps.silkEncodeFrameFIXState
	var out silkEncodeFramePayloadFIXResult
	sc := e.fixedScratch()

	// indices.Seed = frameCounter++ & 3.
	seed := int8(st.frameCounter & 3)
	st.frameCounter++

	x16 := st.xBuf[st.ltpMemLength : st.ltpMemLength+st.frameLength]

	// Analysis chain (VAD, pitch, noise shape, pred coefs, process gains).
	ctrl := e.silkEncodeFrameFIXAnalyze(st)
	out.vadFlag = ctrl.vadFlag

	condCoding := int(st.condCoding)

	// process_gains already produced the iteration-0 gain indices and the
	// dequantized Gains_Q16 (ctrl.gainsQ16). Use them directly; later
	// iterations re-quantize after the gainMult adjustment.
	gainIndices := ensureInt8Slice(&sc.gainIndices, st.nbSubfr)
	copy(gainIndices, ctrl.gainsIndices)
	lastGainIndexPrev := ctrl.lastGainIndexPrev

	// LBRR encoding (before the bitrate loop, like libopus).
	lbrrIndices, lbrrPulses, lbrrFlag := e.silkLBRREncodeFIX(sc, ps, &ctrl, x16, gainIndices, seed)
	if lbrrFlag {
		out.lbrrFlag = 1
		out.lbrrIndices = lbrrIndices
		out.lbrrPulses = lbrrPulses
	}

	re := ps.rangeEncoder
	e.rangeEncoder = re
	e.bandwidth = ps.bandwidth

	// Sync the conditional-pitch coding carry into the encoder for the
	// entropy coder, then snapshot for restore.
	e.ecPrevLagIndex = ps.ecPrevLagIndex
	e.ecPrevSignalType = ps.ecPrevSignalType

	maxBits := ps.maxBits
	useCBR := ps.useCBR
	bitsMargin := 5
	if !useCBR {
		bitsMargin = maxBits / 4
	}

	maxIter := 6
	gainMultQ8 := int16(1 << 8)
	foundLower := false
	foundUpper := false
	gainsID := silkGainsID(gainIndices, st.nbSubfr)
	gainsIDLower := int32(-1)
	gainsIDUpper := int32(-1)

	rangeCopy := *re
	nsqCopy0 := st.nsq
	seedCopy := seed
	ecPrevLagIndexCopy := e.ecPrevLagIndex
	ecPrevSignalTypeCopy := e.ecPrevSignalType
	rangeCopy2 := *re
	var nsqCopy1 NSQState
	var lastGainIndexCopy2 int8
	ecBufCopy := ensureByteSlice(&sc.ecBufCopy, len(re.Buffer()))

	var nBits, nBitsLower, nBitsUpper int
	var gainMultLower, gainMultUpper int32
	var gainLock [maxNbSubfr]bool
	var bestGainMult [maxNbSubfr]int16
	var bestSum [maxNbSubfr]int
	var pulses []int8

	currentPrevInd := st.lastGainIndex
	frameSeed := seed

	for iter := 0; ; iter++ {
		if gainsID == gainsIDLower {
			nBits = nBitsLower
		} else if gainsID == gainsIDUpper {
			nBits = nBitsUpper
		} else {
			if iter > 0 {
				*re = rangeCopy
				st.nsq = nsqCopy0
				frameSeed = seedCopy
				e.ecPrevLagIndex = ecPrevLagIndexCopy
				e.ecPrevSignalType = ecPrevSignalTypeCopy
			}

			// Noise shaping quantization.
			if pulses == nil {
				pulses = ensureInt8Slice(&sc.pulses, st.frameLength)
			}
			idx := ctrlToIndices(&ctrl, gainIndices, frameSeed)
			silkRunNSQFIX(sc, st, &st.nsq, &idx, &ctrl, x16, pulses)
			frameSeed = idx.Seed

			if iter == maxIter && !foundLower {
				rangeCopy2 = *re
			}

			// Encode parameters and excitation.
			idx.Seed = frameSeed
			e.silkEncodeIndices(re, &idx, st.fsKHz, st.nbSubfr, ps.bandwidth, condCoding, false)
			e.encodePulses(pulses, int(idx.signalType), int(idx.quantOffsetType))

			nBits = re.Tell()

			// Damage control on the final iteration.
			if iter == maxIter && !foundLower && nBits > maxBits {
				*re = rangeCopy2
				for i := 0; i < st.nbSubfr; i++ {
					gainIndices[i] = 4
				}
				if condCoding != codeConditionally {
					gainIndices[0] = lastGainIndexPrev
				}
				e.ecPrevLagIndex = ecPrevLagIndexCopy
				e.ecPrevSignalType = ecPrevSignalTypeCopy
				currentPrevInd = lastGainIndexPrev
				for i := range pulses {
					pulses[i] = 0
				}
				idx = ctrlToIndices(&ctrl, gainIndices, frameSeed)
				e.silkEncodeIndices(re, &idx, st.fsKHz, st.nbSubfr, ps.bandwidth, condCoding, false)
				e.encodePulses(pulses, int(idx.signalType), int(idx.quantOffsetType))
				nBits = re.Tell()
			}

			if !useCBR && iter == 0 && nBits <= maxBits {
				break
			}
		}

		if iter == maxIter {
			if foundLower && (gainsID == gainsIDLower || nBits > maxBits) {
				*re = rangeCopy2
				offs := int(rangeCopy2.Offs())
				if offs <= len(ecBufCopy) {
					copy(re.Buffer()[:offs], ecBufCopy[:offs])
				}
				st.nsq = nsqCopy1
				currentPrevInd = lastGainIndexCopy2
			}
			break
		}

		if nBits > maxBits {
			if !foundLower && iter >= 2 {
				ctrl.lambdaQ10 = silkADD_RSHIFT32(ctrl.lambdaQ10, ctrl.lambdaQ10, 1)
				foundUpper = false
				gainsIDUpper = -1
			} else {
				foundUpper = true
				nBitsUpper = nBits
				gainMultUpper = int32(gainMultQ8)
				gainsIDUpper = gainsID
			}
		} else if nBits < maxBits-bitsMargin {
			foundLower = true
			nBitsLower = nBits
			gainMultLower = int32(gainMultQ8)
			if gainsID != gainsIDLower {
				gainsIDLower = gainsID
				rangeCopy2 = *re
				offs := int(rangeCopy2.Offs())
				if offs <= len(ecBufCopy) {
					copy(ecBufCopy[:offs], re.Buffer()[:offs])
				}
				nsqCopy1 = st.nsq
				lastGainIndexCopy2 = currentPrevInd
			}
		} else {
			break
		}

		if !foundLower && nBits > maxBits {
			for i := 0; i < st.nbSubfr; i++ {
				sum := 0
				start := i * st.subfrLength
				end := start + st.subfrLength
				if end > len(pulses) {
					end = len(pulses)
				}
				for j := start; j < end; j++ {
					v := int(pulses[j])
					if v < 0 {
						v = -v
					}
					sum += v
				}
				if iter == 0 || (sum < bestSum[i] && !gainLock[i]) {
					bestSum[i] = sum
					bestGainMult[i] = gainMultQ8
				} else {
					gainLock[i] = true
				}
			}
		}

		if !(foundLower && foundUpper) {
			if nBits > maxBits {
				next := int(gainMultQ8) * 3 / 2
				if next > 1024 {
					next = 1024
				}
				gainMultQ8 = int16(next)
			} else {
				next := int(gainMultQ8) * 4 / 5
				if next < 64 {
					next = 64
				}
				gainMultQ8 = int16(next)
			}
		} else {
			gainMultQ8 = int16(gainMultLower + silkDiv32_16(silkMUL(gainMultUpper-gainMultLower, int32(maxBits-nBitsLower)), int32(nBitsUpper-nBitsLower)))
			upper := silkADD_RSHIFT32(gainMultLower, gainMultUpper-gainMultLower, 2)
			lower := gainMultUpper - ((gainMultUpper - gainMultLower) >> 2)
			if int32(gainMultQ8) > upper {
				gainMultQ8 = int16(upper)
			} else if int32(gainMultQ8) < lower {
				gainMultQ8 = int16(lower)
			}
		}

		for i := 0; i < st.nbSubfr; i++ {
			tmp := gainMultQ8
			if gainLock[i] {
				tmp = bestGainMult[i]
			}
			ctrl.gainsQ16[i] = silk_LSHIFT_SAT32(silk_SMULWB(ctrl.gainsUnqQ16[i], int32(tmp)), 8)
		}

		// Quantize gains.
		st.lastGainIndex = lastGainIndexPrev
		gq := ensureInt32Slice(&sc.gq, st.nbSubfr)
		copy(gq, ctrl.gainsQ16[:st.nbSubfr])
		currentPrevInd = silkGainsQuantInto(gainIndices, gq, lastGainIndexPrev, condCoding == codeConditionally, st.nbSubfr)
		copy(ctrl.gainsQ16[:st.nbSubfr], gq)
		st.lastGainIndex = currentPrevInd
		gainsID = silkGainsID(gainIndices, st.nbSubfr)
	}

	// Persist conditional-pitch carry back to the payload state.
	ps.ecPrevLagIndex = e.ecPrevLagIndex
	ps.ecPrevSignalType = e.ecPrevSignalType
	st.lastGainIndex = currentPrevInd

	// Parameters needed for next frame already set by analyze (prevLag,
	// prevSignalType). first_frame_after_reset cleared here.
	st.firstFrameAfterReset = false

	out.nBytesOut = int(silkRSHIFT(int32(re.Tell()+7), 3))
	return out
}

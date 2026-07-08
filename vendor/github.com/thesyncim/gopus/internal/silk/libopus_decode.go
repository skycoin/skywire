package silk

import "github.com/thesyncim/gopus/internal/rangecoding"

// silkDecoderSetFs configures the per-channel decoder state for an internal
// SILK sample rate of fsKHz (8, 12 or 16). It selects the pitch-contour and
// pitch-lag iCDF tables, the NLSF codebook and LPC order, and resets the
// continuity buffers when the rate changes. Mirrors libopus
// silk/decoder_set_fs.c silk_decoder_set_fs.
func silkDecoderSetFs(st *decoderState, fsKHz int) {
	st.subfrLength = int32(subFrameLengthMs * fsKHz)
	frameLength := st.nbSubfr * st.subfrLength

	if st.fsKHz != int32(fsKHz) || frameLength != st.frameLength {
		if fsKHz == 8 {
			if st.nbSubfr == maxNbSubfr {
				st.pitchContourICDF = silk_pitch_contour_NB_iCDF
			} else {
				st.pitchContourICDF = silk_pitch_contour_10_ms_NB_iCDF
			}
		} else {
			if st.nbSubfr == maxNbSubfr {
				st.pitchContourICDF = silk_pitch_contour_iCDF
			} else {
				st.pitchContourICDF = silk_pitch_contour_10_ms_iCDF
			}
		}

		if st.fsKHz != int32(fsKHz) {
			st.ltpMemLength = int32(ltpMemLengthMs * fsKHz)
			if fsKHz == 8 || fsKHz == 12 {
				st.lpcOrder = minLPCOrder
				st.nlsfCB = &silk_NLSF_CB_NB_MB
			} else {
				st.lpcOrder = maxLPCOrder
				st.nlsfCB = &silk_NLSF_CB_WB
			}
			switch fsKHz {
			case 16:
				st.pitchLagLowBitsICDF = silk_uniform8_iCDF
			case 12:
				st.pitchLagLowBitsICDF = silk_uniform6_iCDF
			case 8:
				st.pitchLagLowBitsICDF = silk_uniform4_iCDF
			}
			st.firstFrameAfterReset = true
			st.lagPrev = 100
			st.lastGainIndex = 10
			st.prevSignalType = typeNoVoiceActivity
			for i := range st.outBuf {
				st.outBuf[i] = 0
			}
			for i := range st.sLPCQ14Buf {
				st.sLPCQ14Buf[i] = 0
			}
			// NOTE: libopus does NOT reset prevNLSF_Q15 on bandwidth change.
			// firstFrameAfterReset=true forces NLSFInterpCoefQ2=4 which disables
			// NLSF interpolation, so the previous NLSF values aren't used anyway.
		}

		st.fsKHz = int32(fsKHz)
		st.frameLength = frameLength
	}
}

// silkDecodeIndices range-decodes all side-information indices for one SILK
// frame: signal type and quantization offset, the per-subframe gain indices,
// the NLSF (LSF) vector indices with interpolation factor, and for voiced
// frames the pitch lag, contour, LTP filter and LTP-scaling indices, plus the
// excitation seed. Mirrors libopus silk/decode_indices.c silk_decode_indices.
func silkDecodeIndices(st *decoderState, rd *rangecoding.Decoder, vadFlag bool, condCoding int) {
	var ix int
	if vadFlag {
		ix = rd.DecodeICDF8Unchecked(silk_type_offset_VAD_iCDF) + 2
	} else {
		ix = rd.DecodeICDF2_8(230)
	}
	st.indices.signalType = int8(ix >> 1)
	st.indices.quantOffsetType = int8(ix & 1)

	if condCoding == codeConditionally {
		st.indices.GainsIndices[0] = int8(rd.DecodeICDF8Linear(silk_delta_gain_iCDF))
	} else {
		msb := rd.DecodeICDF8Unchecked(silk_gain_iCDF[int(st.indices.signalType)])
		lsb := rd.DecodeICDF8Unchecked(silk_uniform8_iCDF)
		st.indices.GainsIndices[0] = int8((msb << 3) + lsb)
	}

	for i := 1; i < int(st.nbSubfr); i++ {
		st.indices.GainsIndices[i] = int8(rd.DecodeICDF8Linear(silk_delta_gain_iCDF))
	}

	cb := st.nlsfCB
	stypeBand := int(st.indices.signalType) >> 1
	order := int(cb.order)
	nVectors := int(cb.nVectors)
	cb1Offset := stypeBand * nVectors
	st.indices.NLSFIndices[0] = int8(rd.DecodeICDF8Linear(cb.cb1ICDF[cb1Offset:]))

	// Use pre-allocated scratch buffers if available
	var ecIx []int16
	var predQ8 []uint8
	if len(st.scratchEcIx) >= maxLPCOrder {
		ecIx = st.scratchEcIx[:maxLPCOrder]
	} else {
		ecIx = make([]int16, maxLPCOrder)
	}
	if len(st.scratchPredQ8) >= maxLPCOrder {
		predQ8 = st.scratchPredQ8[:maxLPCOrder]
	} else {
		predQ8 = make([]uint8, maxLPCOrder)
	}
	silkNLSFUnpack(ecIx, predQ8, cb, int(st.indices.NLSFIndices[0]))

	for i := range order {
		idx := rd.DecodeICDF9_8Slice(cb.ecICDF[int(ecIx[i]):])
		if idx == 0 {
			idx -= rd.DecodeICDF7_8Slice(silk_NLSF_EXT_iCDF)
		} else if idx == 2*nlsfQuantMaxAmplitude {
			idx += rd.DecodeICDF7_8Slice(silk_NLSF_EXT_iCDF)
		}
		st.indices.NLSFIndices[i+1] = int8(idx - nlsfQuantMaxAmplitude)
	}

	if st.nbSubfr == maxNbSubfr {
		st.indices.NLSFInterpCoefQ2 = int8(rd.DecodeICDF8Unchecked(silk_NLSF_interpolation_factor_iCDF))
	} else {
		st.indices.NLSFInterpCoefQ2 = 4
	}

	if st.indices.signalType == typeVoiced {
		decodeAbsolute := true
		if condCoding == codeConditionally && st.ecPrevSignalType == typeVoiced {
			deltaLag := rd.DecodeICDF8Linear(silk_pitch_delta_iCDF)
			if deltaLag > 0 {
				deltaLag -= 9
				st.indices.lagIndex = st.ecPrevLagIndex + int16(deltaLag)
				decodeAbsolute = false
			}
		}
		if decodeAbsolute {
			st.indices.lagIndex = int16(int32(rd.DecodeICDF8Linear(silk_pitch_lag_iCDF)) * (st.fsKHz >> 1))
			st.indices.lagIndex += int16(rd.DecodeICDF8Unchecked(st.pitchLagLowBitsICDF))
		}
		st.ecPrevLagIndex = st.indices.lagIndex
		if len(st.pitchContourICDF) > 8 {
			st.indices.contourIndex = int8(rd.DecodeICDF8Linear(st.pitchContourICDF))
		} else {
			st.indices.contourIndex = int8(rd.DecodeICDF8Unchecked(st.pitchContourICDF))
		}

		st.indices.PERIndex = int8(rd.DecodeICDF8Unchecked(silk_LTP_per_index_iCDF))
		for k := 0; k < int(st.nbSubfr); k++ {
			ltpICDF := silk_LTP_gain_iCDF_ptrs[int(st.indices.PERIndex)]
			if st.indices.PERIndex == 0 {
				st.indices.LTPIndex[k] = int8(rd.DecodeICDF8Unchecked(ltpICDF))
			} else {
				st.indices.LTPIndex[k] = int8(rd.DecodeICDF8Linear(ltpICDF))
			}
		}
		if condCoding == codeIndependently {
			st.indices.LTPScaleIndex = int8(rd.DecodeICDF8Unchecked(silk_LTPscale_iCDF))
		} else {
			st.indices.LTPScaleIndex = 0
		}
	}
	st.ecPrevSignalType = int32(st.indices.signalType)

	st.indices.Seed = int8(rd.DecodeICDF8Unchecked(silk_uniform4_iCDF))
}

// silkDecodeShellSplit decodes one node of the pulse-location shell coder: given
// a parent pulse count p it range-decodes how many pulses fall in the left half
// and returns (left, p-left). Mirrors the per-level decode_split steps of
// libopus silk/shell_coder.c silk_shell_decoder.
//
//go:nosplit
func silkDecodeShellSplit(rd *rangecoding.Decoder, p int32, rows *[17][]uint8) (int16, int16) {
	if p > 0 {
		row := rows[int(p)]
		var split int32
		switch p {
		case 1:
			split = int32(rd.DecodeICDF2_8(row[0]))
		case 2:
			split = int32(rd.DecodeICDF3_8(row[0], row[1]))
		case 3:
			split = int32(rd.DecodeICDF4_8(row[0], row[1], row[2]))
		case 4:
			split = int32(rd.DecodeICDF5_8(row[0], row[1], row[2], row[3]))
		default:
			split = int32(rd.DecodeICDF8Unchecked(row))
		}
		return int16(split), int16(p - split)
	}
	return 0, 0
}

// silkShellDecoder decodes the locations of pulses4 pulses within a 16-sample
// shell block by recursively splitting the count down a binary tree of
// silkDecodeShellSplit calls. Mirrors libopus silk/shell_coder.c
// silk_shell_decoder.
func silkShellDecoder(pulses []int16, rd *rangecoding.Decoder, pulses4 int32) {
	// These are small fixed-size arrays, using stack allocation via array
	var pulses3 [2]int16
	var pulses2 [4]int16
	var pulses1 [8]int16

	pulses3[0], pulses3[1] = silkDecodeShellSplit(rd, pulses4, &silk_shell_code_table3_rows)
	pulses2[0], pulses2[1] = silkDecodeShellSplit(rd, int32(pulses3[0]), &silk_shell_code_table2_rows)

	pulses1[0], pulses1[1] = silkDecodeShellSplit(rd, int32(pulses2[0]), &silk_shell_code_table1_rows)
	pulses[0], pulses[1] = silkDecodeShellSplit(rd, int32(pulses1[0]), &silk_shell_code_table0_rows)
	pulses[2], pulses[3] = silkDecodeShellSplit(rd, int32(pulses1[1]), &silk_shell_code_table0_rows)

	pulses1[2], pulses1[3] = silkDecodeShellSplit(rd, int32(pulses2[1]), &silk_shell_code_table1_rows)
	pulses[4], pulses[5] = silkDecodeShellSplit(rd, int32(pulses1[2]), &silk_shell_code_table0_rows)
	pulses[6], pulses[7] = silkDecodeShellSplit(rd, int32(pulses1[3]), &silk_shell_code_table0_rows)

	pulses2[2], pulses2[3] = silkDecodeShellSplit(rd, int32(pulses3[1]), &silk_shell_code_table2_rows)

	pulses1[4], pulses1[5] = silkDecodeShellSplit(rd, int32(pulses2[2]), &silk_shell_code_table1_rows)
	pulses[8], pulses[9] = silkDecodeShellSplit(rd, int32(pulses1[4]), &silk_shell_code_table0_rows)
	pulses[10], pulses[11] = silkDecodeShellSplit(rd, int32(pulses1[5]), &silk_shell_code_table0_rows)

	pulses1[6], pulses1[7] = silkDecodeShellSplit(rd, int32(pulses2[3]), &silk_shell_code_table1_rows)
	pulses[12], pulses[13] = silkDecodeShellSplit(rd, int32(pulses1[6]), &silk_shell_code_table0_rows)
	pulses[14], pulses[15] = silkDecodeShellSplit(rd, int32(pulses1[7]), &silk_shell_code_table0_rows)
}

// silkDecodeSigns range-decodes and applies the sign of every non-zero pulse,
// selecting the sign iCDF by signal type, quantization offset and the block's
// pulse count. Mirrors libopus silk/decode_pulses.c silk_decode_signs.
func silkDecodeSigns(rd *rangecoding.Decoder, pulses []int16, length int, signalType int, quantOffsetType int, sumPulses []int32) {
	qPtr := 0
	idx := 7 * (quantOffsetType + (signalType << 1))
	icdfPtr := silk_sign_iCDF[idx:]
	blocks := (length + shellCodecFrameLength/2) >> log2ShellCodecFrameLength
	for i := range blocks {
		p := sumPulses[i]
		if p > 0 {
			icdf0 := icdfPtr[silkMinInt(int(p&0x1F), 6)]
			block := pulses[qPtr : qPtr+shellCodecFrameLength]
			pulseSum := 0
			if p>>5 == 0 {
				pulseSum = int(p)
			}
			rd.DecodeICDF2_8SignBlock16(icdf0, (*[shellCodecFrameLength]int16)(block), pulseSum)
		}
		qPtr += shellCodecFrameLength
	}
}

// silkDecodePulses decodes the full excitation pulse vector for one SILK frame:
// the per-block pulse counts (with the LSB-extension escape), the pulse
// locations via the shell coder, the low-order amplitude bits and finally the
// signs. Mirrors libopus silk/decode_pulses.c silk_decode_pulses.
func silkDecodePulses(rd *rangecoding.Decoder, pulses []int16, signalType int, quantOffsetType int, frameLength int) {
	silkDecodePulsesWithScratch(rd, pulses, signalType, quantOffsetType, frameLength, nil, nil)
}

// silkDecodePulsesWithScratch is like silkDecodePulses but uses pre-allocated scratch buffers.
func silkDecodePulsesWithScratch(rd *rangecoding.Decoder, pulses []int16, signalType int, quantOffsetType int, frameLength int, scratchSumPulses, scratchNLshifts []int32) {
	rateLevel := rd.DecodeICDF9_8Slice(silk_rate_levels_iCDF[signalType>>1])
	iter := frameLength >> log2ShellCodecFrameLength
	if iter*shellCodecFrameLength < frameLength {
		iter++
	}

	// Use scratch buffers if provided, otherwise allocate
	var sumPulses, nLshifts []int32
	if scratchSumPulses != nil && len(scratchSumPulses) >= iter {
		sumPulses = scratchSumPulses[:iter]
	} else {
		sumPulses = make([]int32, iter)
	}
	if scratchNLshifts != nil && len(scratchNLshifts) >= iter {
		nLshifts = scratchNLshifts[:iter]
	} else {
		nLshifts = make([]int32, iter)
	}

	cdfPtr := silk_pulses_per_block_iCDF[rateLevel]
	for i := 0; i < iter; i++ {
		nLshifts[i] = 0
		sumPulses[i] = int32(rd.DecodeICDF8Linear(cdfPtr))
		for sumPulses[i] == int32(silkMaxPulses+1) {
			nLshifts[i]++
			table := silk_pulses_per_block_iCDF[nRateLevels-1]
			if nLshifts[i] == 10 {
				table = table[1:]
			}
			sumPulses[i] = int32(rd.DecodeICDF8Linear(table))
		}
	}

	for i := 0; i < iter; i++ {
		off := i * shellCodecFrameLength
		if sumPulses[i] > 0 {
			silkShellDecoder(pulses[off:off+shellCodecFrameLength], rd, sumPulses[i])
		} else {
			for j := range shellCodecFrameLength {
				pulses[off+j] = 0
			}
		}
	}

	for i := 0; i < iter; i++ {
		if nLshifts[i] > 0 {
			nLS := int(nLshifts[i])
			off := i * shellCodecFrameLength
			for k := range shellCodecFrameLength {
				absQ := int32(pulses[off+k])
				for range nLS {
					absQ <<= 1
					absQ += int32(rd.DecodeICDF2_8(120))
				}
				pulses[off+k] = int16(absQ)
			}
			sumPulses[i] |= int32(nLS) << 5
		}
	}

	silkDecodeSigns(rd, pulses, frameLength, signalType, quantOffsetType, sumPulses)
}

// silkDecodePitch turns the decoded absolute pitch-lag index and contour index
// into a per-subframe pitch-lag array, adding the contour-codebook offsets and
// clamping each lag to [pe_min_lag, pe_max_lag]. Mirrors libopus
// silk/decode_pitch.c silk_decode_pitch.
func silkDecodePitch(lagIndex int16, contourIndex int8, pitchL []int32, fsKHz int, nbSubfr int) {
	var lagCB [][]int8
	var cbkSize int
	if fsKHz == 8 {
		if nbSubfr == peMaxNbSubfr {
			lagCB = silk_CB_lags_stage2
			cbkSize = peNbCbksStage2Ext
		} else {
			lagCB = silk_CB_lags_stage2_10_ms
			cbkSize = peNbCbksStage2_10ms
		}
	} else {
		if nbSubfr == peMaxNbSubfr {
			lagCB = silk_CB_lags_stage3
			cbkSize = peNbCbksStage3Max
		} else {
			lagCB = silk_CB_lags_stage3_10_ms
			cbkSize = peNbCbksStage3_10ms
		}
	}
	minLag := peMinLagMs * fsKHz
	maxLag := peMaxLagMs * fsKHz
	lag := minLag + int(lagIndex)
	for k := range nbSubfr {
		idx := max(int(contourIndex), 0)
		if idx >= cbkSize {
			idx = cbkSize - 1
		}
		pitchL[k] = int32(silkLimitInt(lag+int(lagCB[k][idx]), minLag, maxLag))
	}
}

// silkBwExpander applies bandwidth expansion (chirp) to a set of int16 AR
// (LPC) coefficients, moving the poles toward the origin to widen formant
// bandwidths. Used after a lost frame to stabilize the recovered filter.
// Mirrors libopus silk/bwexpander.c silk_bwexpander.
func silkBwExpander(ar []int16, chirpQ16 int32) {
	if len(ar) == 0 {
		return
	}
	chirpMinusOneQ16 := chirpQ16 - 65536
	for i := 0; i < len(ar)-1; i++ {
		ar[i] = int16(silkRSHIFT_ROUND(silkMUL(chirpQ16, int32(ar[i])), 16))
		chirpQ16 += silkRSHIFT_ROUND(silkMUL(chirpQ16, chirpMinusOneQ16), 16)
	}
	ar[len(ar)-1] = int16(silkRSHIFT_ROUND(silkMUL(chirpQ16, int32(ar[len(ar)-1])), 16))
}

// silkDecodeParameters dequantizes the decoded indices into the synthesis
// control parameters: per-subframe gains (Q16), the two interpolated LPC
// coefficient sets (Q12) obtained via NLSF decode and NLSF2A, and for voiced
// frames the pitch lags, LTP filter coefficients (Q14) and LTP scale (Q14). On
// a lost-frame recovery it also bandwidth-expands the LPC. Mirrors libopus
// silk/decode_parameters.c silk_decode_parameters.
func silkDecodeParameters(st *decoderState, ctrl *decoderControl, condCoding int) {
	nbSubfr := int(st.nbSubfr)
	lpcOrder := int(st.lpcOrder)
	silkGainsDequant(&ctrl.GainsQ16, &st.indices.GainsIndices, &st.lastGainIndex, condCoding == codeConditionally, nbSubfr)

	var nlsfQ15 [maxLPCOrder]int16
	silkNLSFDecode(nlsfQ15[:], st.indices.NLSFIndices[:], st.nlsfCB)

	if !silkNLSF2A(ctrl.PredCoefQ12[1][:lpcOrder], nlsfQ15[:lpcOrder], lpcOrder) {
		lpc1 := lsfToLPCDirect(nlsfQ15[:lpcOrder])
		copy(ctrl.PredCoefQ12[1][:], lpc1[:lpcOrder])
	}

	if st.firstFrameAfterReset {
		st.indices.NLSFInterpCoefQ2 = 4
	}
	if st.indices.NLSFInterpCoefQ2 < 4 {
		var nlsf0 [maxLPCOrder]int16
		for i := range lpcOrder {
			diff := int32(nlsfQ15[i]) - int32(st.prevNLSFQ15[i])
			nlsf0[i] = int16(int32(st.prevNLSFQ15[i]) + (int32(st.indices.NLSFInterpCoefQ2) * diff >> 2))
		}
		if !silkNLSF2A(ctrl.PredCoefQ12[0][:lpcOrder], nlsf0[:lpcOrder], lpcOrder) {
			lpc0 := lsfToLPCDirect(nlsf0[:lpcOrder])
			copy(ctrl.PredCoefQ12[0][:], lpc0[:lpcOrder])
		}
	} else {
		copy(ctrl.PredCoefQ12[0][:], ctrl.PredCoefQ12[1][:])
	}

	copy(st.prevNLSFQ15[:], nlsfQ15[:])

	if st.lossCnt != 0 {
		silkBwExpander(ctrl.PredCoefQ12[0][:lpcOrder], int32(bweAfterLossQ16))
		silkBwExpander(ctrl.PredCoefQ12[1][:lpcOrder], int32(bweAfterLossQ16))
	}

	if st.indices.signalType == typeVoiced {
		silkDecodePitch(st.indices.lagIndex, st.indices.contourIndex, ctrl.pitchL[:], int(st.fsKHz), nbSubfr)
		cbk := silk_LTP_vq_ptrs_Q7[st.indices.PERIndex]
		for k := range nbSubfr {
			idx := int(st.indices.LTPIndex[k]) * ltpOrder
			for i := range ltpOrder {
				ctrl.LTPCoefQ14[k*ltpOrder+i] = int16(int32(cbk[idx+i]) << 7)
			}
		}
		ctrl.LTPScaleQ14 = int32(silk_LTPScales_table_Q14[st.indices.LTPScaleIndex])
	} else {
		for i := range ctrl.pitchL {
			ctrl.pitchL[i] = 0
		}
		for i := range ctrl.LTPCoefQ14 {
			ctrl.LTPCoefQ14[i] = 0
		}
		st.indices.PERIndex = 0
		ctrl.LTPScaleQ14 = 0
	}
}

// silkLPCAnalysisFilter runs the whitening (analysis) LPC filter used by the
// decoder to rebuild the LTP state buffer from past output. Mirrors libopus
// silk/LPC_analysis_filter.c silk_LPC_analysis_filter. The order-16 case is
// hoisted into silkLPCAnalysisFilterOrder16 for the WB hot path.
func silkLPCAnalysisFilter(out []int16, in []int16, B []int16, length int, order int) {
	if order == 16 && length >= 16 {
		silkLPCAnalysisFilterOrder16(out, in, B, length)
		return
	}
	for i := range order {
		out[i] = 0
	}
	for ix := order; ix < length; ix++ {
		outQ12 := silkSMULBB(int32(in[ix-1]), int32(B[0]))
		for j := 1; j < order; j++ {
			outQ12 = silkSMLABB(outQ12, int32(in[ix-1-j]), int32(B[j]))
		}
		outQ12 = silkLSHIFT(int32(in[ix]), 12) - outQ12
		out32 := silkRSHIFT_ROUND(outQ12, 12)
		out[ix] = silkSAT16(out32)
	}
}

func silkLPCAnalysisFilterOrder16(out []int16, in []int16, B []int16, length int) {
	_ = out[length-1]
	_ = in[length-1]
	_ = B[15]

	clear(out[:16])

	b0 := int32(B[0])
	b1 := int32(B[1])
	b2 := int32(B[2])
	b3 := int32(B[3])
	b4 := int32(B[4])
	b5 := int32(B[5])
	b6 := int32(B[6])
	b7 := int32(B[7])
	b8 := int32(B[8])
	b9 := int32(B[9])
	b10 := int32(B[10])
	b11 := int32(B[11])
	b12 := int32(B[12])
	b13 := int32(B[13])
	b14 := int32(B[14])
	b15 := int32(B[15])

	for ix := 16; ix < length; ix++ {
		outQ12 := int32(in[ix-1]) * b0
		outQ12 += int32(in[ix-2]) * b1
		outQ12 += int32(in[ix-3]) * b2
		outQ12 += int32(in[ix-4]) * b3
		outQ12 += int32(in[ix-5]) * b4
		outQ12 += int32(in[ix-6]) * b5
		outQ12 += int32(in[ix-7]) * b6
		outQ12 += int32(in[ix-8]) * b7
		outQ12 += int32(in[ix-9]) * b8
		outQ12 += int32(in[ix-10]) * b9
		outQ12 += int32(in[ix-11]) * b10
		outQ12 += int32(in[ix-12]) * b11
		outQ12 += int32(in[ix-13]) * b12
		outQ12 += int32(in[ix-14]) * b13
		outQ12 += int32(in[ix-15]) * b14
		outQ12 += int32(in[ix-16]) * b15
		outQ12 = (int32(in[ix]) << 12) - outQ12
		out[ix] = silkSAT16(silkRSHIFT_ROUND(outQ12, 12))
	}
}

// decodeCoreBuffers holds the pre-allocated or newly allocated buffers used in silkDecodeCore.
type decodeCoreBuffers struct {
	sLPC       []int32
	sLTP       []int16
	sLTP_Q15   []int32
	sLTPBufIdx int
}

// initDecodeCoreBuffers initializes the scratch buffers for silkDecodeCore.
// This eliminates hot-path allocations when called from the Decoder.
func initDecodeCoreBuffers(st *decoderState) decodeCoreBuffers {
	var bufs decodeCoreBuffers
	subfrLength := int(st.subfrLength)
	ltpMemLength := int(st.ltpMemLength)
	frameLength := int(st.frameLength)

	// sLPC buffer
	if st.scratchSLPC != nil && len(st.scratchSLPC) >= subfrLength+maxLPCOrder {
		bufs.sLPC = st.scratchSLPC[:subfrLength+maxLPCOrder]
	} else {
		bufs.sLPC = make([]int32, subfrLength+maxLPCOrder)
	}
	copy(bufs.sLPC, st.sLPCQ14Buf[:])

	// sLTP buffer
	if st.scratchSLTP != nil && len(st.scratchSLTP) >= ltpMemLength {
		bufs.sLTP = st.scratchSLTP[:ltpMemLength]
	} else {
		bufs.sLTP = make([]int16, ltpMemLength)
	}

	// sLTP_Q15 buffer
	if st.scratchSLTPQ15 != nil && len(st.scratchSLTPQ15) >= ltpMemLength+frameLength {
		bufs.sLTP_Q15 = st.scratchSLTPQ15[:ltpMemLength+frameLength]
	} else {
		bufs.sLTP_Q15 = make([]int32, ltpMemLength+frameLength)
	}

	bufs.sLTPBufIdx = ltpMemLength
	return bufs
}

// processLTPVoiced handles the LTP processing for voiced signals in the decoder core.
// It computes sLTP_Q15 values from the LPC analysis filter or applies gain adjustment.
// Parameters:
//   - st: decoder state
//   - ctrl: decoder control parameters
//   - out: output buffer (used when k==2 to copy samples to outBuf)
//   - A_Q12: LPC prediction coefficients
//   - sLTP: short-term prediction buffer
//   - sLTP_Q15: short-term prediction buffer in Q15
//   - sLTPBufIdx: current index in sLTP_Q15 buffer
//   - lag: pitch lag for current subframe
//   - k: subframe index
//   - interpFlag: whether interpolation is active
//   - invGainQ31: inverse gain in Q31 (modified for k==0 with LTP scale)
//   - gainAdjQ16: gain adjustment factor
//
// Returns the potentially modified invGainQ31 value.
func processLTPVoiced(
	st *decoderState,
	ctrl *decoderControl,
	out []int16,
	A_Q12 []int16,
	sLTP []int16,
	sLTP_Q15 []int32,
	sLTPBufIdx int,
	lag int,
	k int,
	interpFlag bool,
	invGainQ31 int32,
	gainAdjQ16 int32,
) int32 {
	if k == 0 || (k == 2 && interpFlag) {
		ltpMemLength := int(st.ltpMemLength)
		lpcOrder := int(st.lpcOrder)
		subfrLength := int(st.subfrLength)
		startIdx := ltpMemLength - lag - lpcOrder - ltpOrder/2
		if startIdx <= 0 {
			startIdx = 1 // Match libopus assertion: start_idx > 0
		}
		if k == 2 {
			copy(st.outBuf[ltpMemLength:], out[:2*subfrLength])
		}
		silkLPCAnalysisFilter(sLTP[startIdx:], st.outBuf[startIdx+k*subfrLength:], A_Q12, ltpMemLength-startIdx, lpcOrder)
		if k == 0 {
			invGainQ31 = silkLSHIFT(silkSMULWB(invGainQ31, ctrl.LTPScaleQ14), 2)
		}
		for i := 0; i < lag+ltpOrder/2; i++ {
			sLTP_Q15[sLTPBufIdx-i-1] = silkSMULWB(invGainQ31, int32(sLTP[ltpMemLength-i-1]))
		}
	} else if gainAdjQ16 != int32(1<<16) {
		for i := 0; i < lag+ltpOrder/2; i++ {
			sLTP_Q15[sLTPBufIdx-i-1] = silkSMULWW(gainAdjQ16, sLTP_Q15[sLTPBufIdx-i-1])
		}
	}
	return invGainQ31
}

// lShiftSAT32By4 is a decode-hot specialization of silkLShiftSAT32(x, 4).
// It keeps exact saturation behavior while avoiding int64 work for this fixed shift.
func lShiftSAT32By4(x int32) int32 {
	if x > 0x07ffffff {
		return 0x7fffffff
	}
	if x < -0x08000000 {
		return -0x80000000
	}
	return x << 4
}

// synthesizeLPCOrder10 computes LPC synthesis for NB/MB (order 10).
// This is a decode-core hot path and keeps the exact operation order.
func synthesizeLPCOrder10(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	c0 := int32(A_Q12[0])
	c1 := int32(A_Q12[1])
	c2 := int32(A_Q12[2])
	c3 := int32(A_Q12[3])
	c4 := int32(A_Q12[4])
	c5 := int32(A_Q12[5])
	c6 := int32(A_Q12[6])
	c7 := int32(A_Q12[7])
	c8 := int32(A_Q12[8])
	c9 := int32(A_Q12[9])

	// Keep the last 10 samples in locals and slide every iteration.
	// This removes repeated indexed loads from sLPC in the hot loop.
	v0 := sLPC[maxLPCOrder-1]
	v1 := sLPC[maxLPCOrder-2]
	v2 := sLPC[maxLPCOrder-3]
	v3 := sLPC[maxLPCOrder-4]
	v4 := sLPC[maxLPCOrder-5]
	v5 := sLPC[maxLPCOrder-6]
	v6 := sLPC[maxLPCOrder-7]
	v7 := sLPC[maxLPCOrder-8]
	v8 := sLPC[maxLPCOrder-9]
	v9 := sLPC[maxLPCOrder-10]

	sIdx := maxLPCOrder
	for i := range subfrLength {
		lpcPredQ10 := int32(minLPCOrder >> 1)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v0, c0)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v1, c1)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v2, c2)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v3, c3)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v4, c4)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v5, c5)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v6, c6)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v7, c7)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v8, c8)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v9, c9)

		s := silkAddSat32(presQ14[i], lShiftSAT32By4(lpcPredQ10))
		sLPC[sIdx] = s
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(s, gainQ10), 8))
		sIdx++

		v9 = v8
		v8 = v7
		v7 = v6
		v6 = v5
		v5 = v4
		v4 = v3
		v3 = v2
		v2 = v1
		v1 = v0
		v0 = s
	}
}

// synthesizeLPCOrder16 computes LPC synthesis for WB (order 16).
// This is a decode-core hot path and keeps the exact operation order.
func synthesizeLPCOrder16(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	synthesizeLPCOrder16Core(sLPC, A_Q12, presQ14, pxq, gainQ10, subfrLength)
}

func synthesizeLPCGeneric(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength, order int) {
	for i := range subfrLength {
		lpcPredQ10 := int32(order >> 1)
		for j := range order {
			lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPC[maxLPCOrder+i-j-1], int32(A_Q12[j]))
		}
		s := silkAddSat32(presQ14[i], lShiftSAT32By4(lpcPredQ10))
		sLPC[maxLPCOrder+i] = s
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(s, gainQ10), 8))
	}
}

// silkDecodeCore reconstructs one SILK frame's PCM from the decoded pulses and
// control parameters. It rebuilds the gain-scaled excitation (applying the
// quantization offset, rate-dither adjustment and per-sample sign from the LCG
// seed), then for each subframe applies long-term prediction (voiced) followed
// by short-term LPC synthesis, carrying the LPC and LTP state across subframes.
// Mirrors libopus silk/decode_core.c silk_decode_core.
func silkDecodeCore(st *decoderState, ctrl *decoderControl, out []int16, pulses []int16) {
	offsetQ10 := silk_Quantization_Offsets_Q10[int(st.indices.signalType)>>1][int(st.indices.quantOffsetType)]
	interpFlag := st.indices.NLSFInterpCoefQ2 < 4
	frameLength := int(st.frameLength)
	nbSubfr := int(st.nbSubfr)
	subfrLength := int(st.subfrLength)
	lpcOrder := int(st.lpcOrder)

	randSeed := int32(st.indices.Seed)
	offsetQ14 := int32(offsetQ10) << 4
	quantAdjustQ14 := int32(quantLevelAdjustQ10 << 4)
	for i := range frameLength {
		randSeed = silkRand(randSeed)
		pulse := int32(pulses[i])
		exc := pulse << 14
		negPulseMask := pulse >> 31
		posPulseMask := (-pulse) >> 31
		exc += (negPulseMask & quantAdjustQ14) - (posPulseMask & quantAdjustQ14)
		exc += offsetQ14
		seedMask := randSeed >> 31
		exc = (exc ^ seedMask) - seedMask
		st.excQ14[i] = exc
		randSeed += pulse
	}

	bufs := initDecodeCoreBuffers(st)
	sLPC := bufs.sLPC
	sLTP := bufs.sLTP
	sLTP_Q15 := bufs.sLTP_Q15
	sLTPBufIdx := bufs.sLTPBufIdx
	pexc := st.excQ14[:]
	pxq := out

	for k := range nbSubfr {
		A_Q12 := ctrl.PredCoefQ12[k>>1][:]
		B_Q14 := ctrl.LTPCoefQ14[k*ltpOrder : (k+1)*ltpOrder]
		signalType := int(st.indices.signalType)

		gainQ10 := ctrl.GainsQ16[k] >> 6
		invGainQ31 := silkInverse32VarQ(ctrl.GainsQ16[k], 47)
		gainAdjQ16 := int32(1 << 16)

		if ctrl.GainsQ16[k] != st.prevGainQ16 {
			gainAdjQ16 = silkDiv32VarQ(st.prevGainQ16, ctrl.GainsQ16[k], 16)
			for i := range maxLPCOrder {
				sLPC[i] = silkSMULWW(gainAdjQ16, sLPC[i])
			}
		}
		st.prevGainQ16 = ctrl.GainsQ16[k]

		if st.lossCnt != 0 && st.prevSignalType == typeVoiced && signalType != typeVoiced && k < maxNbSubfr/2 {
			for i := range ltpOrder {
				B_Q14[i] = 0
			}
			B_Q14[ltpOrder/2] = int16(silkFixConst(0.25, 14))
			signalType = typeVoiced
			ctrl.pitchL[k] = st.lagPrev
		}

		if signalType == typeVoiced {
			lag := int(ctrl.pitchL[k])
			processLTPVoiced(st, ctrl, out, A_Q12, sLTP, sLTP_Q15, sLTPBufIdx, lag, k, interpFlag, invGainQ31, gainAdjQ16)
		}

		var presQ14 []int32
		if signalType == typeVoiced {
			lag := int(ctrl.pitchL[k])
			predLagPtr := sLTPBufIdx - lag + ltpOrder/2
			// Use pre-allocated presQ14 buffer if available
			if st.scratchPresQ14 != nil && len(st.scratchPresQ14) >= subfrLength {
				presQ14 = st.scratchPresQ14[:subfrLength]
			} else {
				presQ14 = make([]int32, subfrLength)
			}
			for i := range subfrLength {
				ltpPredQ13 := int32(2)
				ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTP_Q15[predLagPtr+0], int32(B_Q14[0]))
				ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTP_Q15[predLagPtr-1], int32(B_Q14[1]))
				ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTP_Q15[predLagPtr-2], int32(B_Q14[2]))
				ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTP_Q15[predLagPtr-3], int32(B_Q14[3]))
				ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTP_Q15[predLagPtr-4], int32(B_Q14[4]))
				predLagPtr++
				presQ14[i] = silkADD_LSHIFT32(pexc[i], ltpPredQ13, 1)
				sLTP_Q15[sLTPBufIdx] = silkLSHIFT(presQ14[i], 1)
				sLTPBufIdx++
			}
		} else {
			presQ14 = pexc[:subfrLength]
		}

		switch lpcOrder {
		case minLPCOrder:
			synthesizeLPCOrder10(sLPC, A_Q12, presQ14, pxq, gainQ10, subfrLength)
		case maxLPCOrder:
			synthesizeLPCOrder16(sLPC, A_Q12, presQ14, pxq, gainQ10, subfrLength)
		default:
			synthesizeLPCGeneric(sLPC, A_Q12, presQ14, pxq, gainQ10, subfrLength, lpcOrder)
		}

		copy(sLPC, sLPC[subfrLength:subfrLength+maxLPCOrder])
		pexc = pexc[subfrLength:]
		pxq = pxq[subfrLength:]
	}

	copy(st.sLPCQ14Buf[:], sLPC[:maxLPCOrder])
}

// silkRand advances the SILK linear congruential generator used to derive
// excitation signs and CNG noise. Mirrors the silk_RAND macro in
// libopus silk/SigProc_FIX.h.
func silkRand(seed int32) int32 {
	return seed*196314165 + 907633515
}

// silkUpdateOutBuf shifts the decoder output history buffer (outBuf) by one
// frame and appends the freshly decoded frame at the end, preserving the
// ltp_mem_length samples the next frame's LTP analysis filter needs. Mirrors
// the out_buf update at the bottom of libopus silk/decode_core.c.
func silkUpdateOutBuf(st *decoderState, frame []int16) {
	if st.ltpMemLength == 0 || st.frameLength == 0 {
		return
	}
	if st.ltpMemLength < st.frameLength {
		return
	}
	ltpMemLength := int(st.ltpMemLength)
	frameLength := int(st.frameLength)
	mvLen := ltpMemLength - frameLength
	buf := st.outBuf[:]
	if mvLen > 0 {
		copy(buf, buf[frameLength:frameLength+mvLen])
	}
	copy(buf[mvLen:mvLen+frameLength], frame)
}

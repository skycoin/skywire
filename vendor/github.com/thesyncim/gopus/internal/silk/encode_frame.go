package silk

import "math"

const (
	speechActivityDTXThresholdQ8   = 13   // SILK_FIX_CONST(0.05, 8)
	bandwidthSwitchDelaySlopeQ24Q8 = 3188 // SILK_FIX_CONST((1 - 0.05) / 5000, 24)
	maxBandwidthSwitchDelayMS      = 5000
)

// EncodeFrame encodes a complete SILK frame to bitstream.
// Returns encoded bytes. If a range encoder was pre-set via SetRangeEncoder(),
// it will be used (for hybrid mode) and nil is returned since the caller
// manages the shared encoder.
//
// Reference: libopus silk/float/encode_frame_FLP.c
func (e *Encoder) EncodeFrame(pcm []float32, lookahead []float32, vadFlag bool) []byte {
	config := GetBandwidthConfig(e.bandwidth)
	useSharedEncoder := e.rangeEncoder != nil
	blockUseCBR := e.blockUseCBR
	if e.nFramesPerPacket <= 1 && !useSharedEncoder {
		blockUseCBR = e.useCBR
	}
	subframeSamples := config.SubframeSamples
	numSubframes := len(pcm) / subframeSamples
	if numSubframes < 1 {
		numSubframes = 1
		subframeSamples = len(pcm)
	}
	if numSubframes > maxNbSubfr {
		numSubframes = maxNbSubfr
	}
	frameSamples := min(numSubframes*subframeSamples, len(pcm))
	payloadSizeMs := (frameSamples * 1000) / config.SampleRate
	packetPayloadSizeMs := payloadSizeMs
	if e.nFramesPerPacket > 1 {
		packetPayloadSizeMs = payloadSizeMs * int(e.nFramesPerPacket)
	}

	// Match libopus packet control ordering:
	// packet/frame counters are reset before target-rate/SNR control for
	// standalone packets, so bitsBalance only applies to already-encoded
	// frames within the current packet.
	if !useSharedEncoder {
		e.ResetPacketState()
		e.nFramesPerPacket = 1
	}
	firstFrameAfterReset := e.firstFrameAfterResetActive()

	// Match libopus silk/control_codec.c: when fs_kHz changes (including on the
	// very first call, since fs_kHz starts at 0 after memset-init), the encoder
	// resets sShape.LastGainIndex to 10, sNSQ.lagPrev to 100, etc.  In gopus
	// the constructor leaves previousGainIndex at 0 (matching silk_init_encoder's
	// memset), and we apply the control_encoder initialization here before the
	// actual encoding uses the post-control_encoder state (10).
	if firstFrameAfterReset {
		e.previousGainIndex = 10
		// control_codec sets sNSQ.prev_gain_Q16 during first-frame reset path.
		if e.nsqState != nil && e.nsqState.prevGainQ16 == 0 {
			e.nsqState.prevGainQ16 = 1 << 16
		}
		// Match libopus control_codec.c:254,257: prevLag and lagPrev are set to
		// 100 when fs_kHz changes (including the very first call).  These affect
		// pitch prediction and LTP state in the NSQ loop for the first frame.
		e.pitchState.prevLag = 100
		if e.nsqState != nil {
			e.nsqState.lagPrev = 100
		}
	}

	// Fold this frame's LBRR header bits into the nBitsUsedLBRR exponential moving
	// average. libopus enc_API.c does this every frame of the packet inside the
	// rate-control loop (lines 414-425): curr_nBitsUsedLBRR is re-zeroed each
	// iteration, so only the frame that wrote the packet's LBRR header has non-zero
	// bits. Frames after it therefore see curr < 10 and reset the EMA to zero,
	// which is what stops them from subtracting the (already paid for) LBRR
	// overhead from their own target rate. The encodeLBRRData header writer stores
	// the raw count in currNBitsUsedLBRR; consume + zero it here. Stereo packets
	// run this update in their own orchestration before stereoAllocationTargetRate,
	// so skip the per-channel encode body to avoid a second update with curr==0.
	if e.stereoCondMid == nil {
		e.applyLBRRReservoirUpdate()
	}

	// Update target SNR based on configured bitrate and frame size.
	// Matches libopus silk/enc_API.c rate control logic (lines 411-443).
	if e.preAdjustedTargetRateBps > 0 {
		// The pre-adjusted target is a per-channel rate already produced by the
		// stereo orchestration's silk_stereo_LR_to_MS split (channelRate_bps in
		// libopus enc_API.c). libopus feeds it straight into silk_control_SNR with
		// no [5000, bitRate] clamp: the LIMIT(TargetRate_bps, bitRate, 5000) is
		// applied to the pre-split total, not the per-channel rates. The side
		// channel routinely lands below 5000 (e.g. 2750 bps), which must map to
		// SNR_dB_Q7 == 0; clamping it up to 5000 here over-shapes the side frame
		// and diverges from libopus.
		targetRate := e.preAdjustedTargetRateBps
		e.lastControlTargetRateBps = targetRate
		e.controlSNR(int(targetRate), numSubframes)
		e.preAdjustedTargetRateBps = 0
	} else if e.targetRateBps > 0 {
		// Total target bits for packet
		nBits := (e.targetRateBps * int32(packetPayloadSizeMs)) / 1000

		// Subtract bits used for LBRR (exponential moving average).
		// Matches libopus enc_API.c line 425: nBits -= psEnc->nBitsUsedLBRR;
		nBits -= e.nBitsUsedLBRR

		// Divide by number of uncoded frames left in packet
		nBits /= e.nFramesPerPacket

		// Convert to bits/second
		targetRate := int32(0)
		if payloadSizeMs == 10 {
			targetRate = nBits * 100
		} else {
			targetRate = nBits * 50
		}

		// Subtract fraction of bits in excess of target in previous packets.
		// Matches libopus enc_API.c line 436.
		targetRate -= (e.nBitsExceeded * 1000) / 500

		// Compare actual vs target bits so far in this packet (bitsBalance).
		// Matches libopus enc_API.c lines 437-440:
		//   bitsBalance = ec_tell(psRangeEnc) - psEnc->nBitsUsedLBRR - nBits * nFramesEncoded
		if e.nFramesEncoded > 0 && e.rangeEncoder != nil {
			bitsBalance := int32(e.rangeEncoder.Tell()) - e.nBitsUsedLBRR - nBits*e.nFramesEncoded
			targetRate -= (bitsBalance * 1000) / 500
		}

		// Never exceed input bitrate, and maintain minimum for quality.
		if targetRate > e.targetRateBps {
			targetRate = e.targetRateBps
		}
		if targetRate < 5000 {
			targetRate = 5000
		}

		e.lastControlTargetRateBps = targetRate
		e.controlSNR(int(targetRate), numSubframes)
	} else {
		e.lastControlTargetRateBps = 0
	}

	// Quantize input to int16 precision to match libopus float API behavior.
	pcm = e.quantizePCMToInt16(pcm)

	// Apply LP variable cutoff filter for smooth bandwidth transitions.
	// Matches libopus encode_frame_FLP.c line 134: silk_LP_variable_cutoff(&sLP, inputBuf+1, frame_length).
	// The filter operates on int16 data, so we convert float32→int16, filter, then int16→float32.
	// Only do the conversion when the filter is active (Mode != 0); when Mode == 0 it's a no-op.
	if e.lpState.Mode != 0 {
		lpBuf := ensureInt16Slice(&e.scratchLPInt16, frameSamples)
		scale := float32(silkSampleScale)
		for i := range frameSamples {
			lpBuf[i] = int16(floatToInt16Round(pcm[i] * scale))
		}
		e.lpState.LPVariableCutoff(lpBuf, frameSamples)
		invScale := float32(1.0 / silkSampleScale)
		for i := range frameSamples {
			pcm[i] = float32(lpBuf[i]) * invScale
		}
	}

	if !useSharedEncoder {
		e.rangeEncoder = nil // Safety clear
		bufSize := max(len(pcm)/3, 80)
		if e.lbrrEnabled {
			bufSize += 50
		}
		if bufSize < maxSilkPacketBytes {
			bufSize = maxSilkPacketBytes
		}
		output := ensureByteSlice(&e.scratchOutput, bufSize)
		e.scratchRangeEncoder.Init(output)
		e.rangeEncoder = &e.scratchRangeEncoder
		e.encodeLBRRData(e.rangeEncoder, 1, true)
	}

	var condCoding int
	if e.stereoCondMid != nil {
		condCoding = stereoSelectCondCoding(int(e.stereoCondMidFramesEncoded), int(e.stereoChannelIdx), e.stereoPrevDecodeOnlyMiddle)
	} else {
		condCoding = codeIndependently
		if e.nFramesEncoded > 0 {
			condCoding = codeConditionally
		}
	}

	// Under the gopus_fixed_point build the SILK analysis + rate-control body is
	// driven by the bit-exact FIXED_POINT chain instead of the float analysis.
	// The surrounding orchestration (range-coder init, LBRR header, multi-frame
	// loop, VAD/FEC header patch) is shared with the float path.
	if e.fixedEncodeActive() {
		return e.encodeFrameFixedBody(pcm, frameSamples, numSubframes, subframeSamples,
			payloadSizeMs, condCoding, vadFlag, firstFrameAfterReset, useSharedEncoder, blockUseCBR)
	}

	// Step 1: Determine activity and defaults
	var signalType, quantOffset int
	speechActivityQ8 := int32(0)
	if vadFlag {
		signalType = typeUnvoiced
		quantOffset = 0
		if e.speechActivitySet {
			speechActivityQ8 = e.speechActivityQ8
		} else {
			speechActivityQ8 = 200
		}
	} else {
		signalType = typeNoVoiceActivity
		quantOffset = 0
		if e.speechActivitySet {
			speechActivityQ8 = e.speechActivityQ8
		} else {
			speechActivityQ8 = 50
		}
	}
	e.lastSpeechActivityQ8 = speechActivityQ8

	// Step 1.1: Update noise shaping lookahead buffer and select delayed frame
	framePCM := e.updateShapeBuffer(pcm, frameSamples)

	// Step 2: Pitch detection and LTP
	var pitchLags []int32
	var lagIndex, contourIndex int
	var pitchParams pitchEncodeParams
	var ltpCoeffs LTPCoeffsArray
	ltpScaleIndex := 0
	var ltpIndices [maxNbSubfr]int8
	perIndex := 0
	predGainQ7 := int32(0)
	pitchRes, resStart, _, pitchInfo := e.computePitchResidual(numSubframes)
	if signalType != typeNoVoiceActivity {
		searchThres1 := float32(e.pitchEstimationThresholdQ16) / 65536.0
		prevSignalType := 0
		if e.isPreviousFrameVoiced {
			prevSignalType = 2
		}
		thrhldF32 := float32(0.6)
		thrhldF32 -= float32(0.004) * float32(e.pitchEstimationLPCOrder)
		thrhldF32 -= float32(0.1) * float32(speechActivityQ8) * (1.0 / 256.0)
		thrhldF32 -= float32(0.15) * float32(prevSignalType>>1)
		thrhldF32 -= float32(0.1) * float32(e.inputTiltQ15) * (1.0 / 32768.0)
		if thrhldF32 < 0 {
			thrhldF32 = 0
		} else if thrhldF32 > 1 {
			thrhldF32 = 1
		}
		if firstFrameAfterReset {
			pitchLags = make([]int32, numSubframes)
			e.ltpCorr = 0
			e.pitchState.ltpCorr = 0
		} else {
			pitchLags, lagIndex, contourIndex = e.detectPitch(pitchRes, numSubframes, searchThres1, thrhldF32)
			e.ltpCorr = e.pitchState.ltpCorr
			if e.ltpCorr > 1.0 {
				e.ltpCorr = 1.0
			}
			if e.ltpCorr > 0 {
				signalType = typeVoiced
				pitchParams = e.preparePitchLags(pitchLags, numSubframes, lagIndex, contourIndex)
				ltpCoeffs, ltpIndices, perIndex, predGainQ7 = e.analyzeLTPQuantized(pitchRes, resStart, pitchLags, numSubframes, subframeSamples)
				ltpScaleIndex = e.computeLTPScaleIndex(predGainQ7, condCoding)
			} else {
				signalType = typeUnvoiced
				e.sumLogGainQ7 = 0
			}
		}
	} else {
		e.ltpCorr = 0
		e.pitchState.ltpCorr = 0
		e.pitchState.prevLag = 0
		e.sumLogGainQ7 = 0
	}

	// Step 3: Noise shaping analysis
	noiseParams, gains, quantOffset := e.noiseShapeAnalysis(framePCM, pitchRes, resStart, signalType, int(speechActivityQ8), e.lastLPCGain, pitchLags, quantOffset, numSubframes, subframeSamples)

	// Step 4: Build LTP residual and compute LPC
	fsKHz := config.SampleRate / 1000
	ltpMemSamples := ltpMemLengthMs * fsKHz
	pitchBuf := e.inputBuffer
	frameStart := ltpMemSamples
	if frameStart+frameSamples > len(pitchBuf) {
		if len(pitchBuf) > frameSamples {
			frameStart = len(pitchBuf) - frameSamples
		} else {
			frameStart = 0
		}
	}
	ltpRes := e.buildLTPResidual(pitchBuf, frameStart, gains, pitchLags, ltpCoeffs, numSubframes, subframeSamples, signalType)
	codingQuality := float32(0.0)
	if noiseParams != nil {
		codingQuality = noiseParams.CodingQuality
	}
	minInvGainVal := computeMinInvGain(predGainQ7, codingQuality, firstFrameAfterReset)
	lpcQ12, lsfQ15, interpIdx := e.computeLPCAndNLSFWithInterp(ltpRes, numSubframes, subframeSamples, minInvGainVal)
	stage1Idx, residuals, interpIdx := e.quantizeLSFWithInterp(lsfQ15, e.bandwidth, signalType, int(speechActivityQ8), numSubframes, interpIdx)
	lsfQ15 = e.decodeQuantizedNLSF(stage1Idx, residuals, e.bandwidth)
	predCoefQ12 := ensureInt16Slice(&e.scratchPredCoefQ12, 2*maxLPCOrder)
	interpIdx = e.buildPredCoefQ12(predCoefQ12, lsfQ15, interpIdx)

	// Step 6: Residual energy and gain processing
	var traceGainsPreQ16, traceResNrgBits, traceGainsUnqQ16, traceGainsQuantQ16 [maxNbSubfr]int32
	tracePredGainBits := int32(0)
	tracePitchAutoCorr0Bits := int32(0)
	tracePitchResNrgBits := int32(0)
	if encodeFrameTraceEnabled {
		tracePredGainBits = int32(math.Float32bits(pitchInfo.predGain))
		tracePitchAutoCorr0Bits = int32(math.Float32bits(pitchInfo.autoCorr0))
		tracePitchResNrgBits = int32(math.Float32bits(pitchInfo.resNrg))
		for i := 0; i < numSubframes && i < maxNbSubfr && i < len(gains); i++ {
			traceGainsPreQ16[i] = int32(gains[i] * 65536.0)
		}
	}
	resNrg := e.computeResidualEnergies(ltpRes, predCoefQ12, interpIdx, gains, numSubframes, subframeSamples)
	if encodeFrameTraceEnabled {
		for i := 0; i < numSubframes && i < maxNbSubfr && i < len(resNrg); i++ {
			traceResNrgBits[i] = int32(math.Float32bits(resNrg[i]))
		}
	}
	processedQuantOffset := applyGainProcessing(gains, resNrg, predGainQ7, int(e.snrDBQ7), signalType, int(e.inputTiltQ15), subframeSamples)
	if signalType == typeVoiced {
		quantOffset = processedQuantOffset
	}
	if noiseParams != nil {
		noiseParams.LambdaQ10 = computeLambdaQ10(signalType, int(speechActivityQ8), quantOffset, int(e.nStatesDelayedDecision), noiseParams.CodingQuality, noiseParams.InputQuality)
	}

	// Step 7: Prepare indices and gains for bitrate control loop.
	// Match libopus silk_encode_frame_{FLP,FIX}: the frame counter advances
	// when the entropy seed is selected, before the prefill/encode branch exits.
	seed := e.frameCounter & 3
	e.frameCounter++
	maxBits := int(e.maxBits)
	if maxBits <= 0 {
		// Derive from target rate: bits = targetRate * frameDuration_ms / 1000
		// This matches libopus where opus_encoder.c computes maxBits from the
		// packet budget before calling silk_Encode. When the SILK encoder is
		// invoked standalone (not via the Opus-level encoder), maxBits would
		// otherwise default to an astronomically large value that defeats the
		// rate control loop (VBR fast-exit always triggers on iteration 0).
		if e.targetRateBps > 0 && payloadSizeMs > 0 {
			maxBits = int(e.targetRateBps) * payloadSizeMs / 1000
		} else {
			// Fallback: use a generous but reasonable default (1275 bytes = max SILK packet)
			maxBits = 1275 * 8
		}
		if maxBits < 100 {
			maxBits = 100
		}
	}
	var frameIndices sideInfoIndices
	frameIndices.signalType = int8(signalType)
	frameIndices.quantOffsetType = int8(quantOffset)
	frameIndices.NLSFInterpCoefQ2 = int8(interpIdx)
	frameIndices.PERIndex = int8(perIndex)
	frameIndices.LTPScaleIndex = int8(ltpScaleIndex)
	frameIndices.Seed = int8(seed)
	frameIndices.lagIndex = int16(pitchParams.lagIdx)
	frameIndices.contourIndex = int8(pitchParams.contourIdx)
	if stage1Idx < 0 {
		frameIndices.NLSFIndices[0] = 0
	} else {
		frameIndices.NLSFIndices[0] = int8(stage1Idx)
	}
	for i := 0; i < len(residuals) && i < maxLPCOrder; i++ {
		frameIndices.NLSFIndices[i+1] = int8(residuals[i])
	}
	for i := 0; i < numSubframes && i < len(ltpIndices); i++ {
		frameIndices.LTPIndex[i] = ltpIndices[i]
	}

	gainsUnqQ16 := ensureInt32Slice(&e.scratchGainsUnqQ16, numSubframes)
	for i := 0; i < numSubframes && i < len(gains); i++ {
		gainsUnqQ16[i] = int32(gains[i] * 65536.0)
	}
	gainsQ16 := ensureInt32Slice(&e.scratchGainsQ16, numSubframes)
	copy(gainsQ16, gainsUnqQ16)
	gainIndices := ensureInt8Slice(&e.scratchGainInd, numSubframes)
	lastGainIndexPrev := e.previousGainIndex
	currentPrevInd := silkGainsQuantInto(gainIndices, gainsQ16, lastGainIndexPrev, condCoding == codeConditionally, numSubframes)
	for i := 0; i < numSubframes; i++ {
		frameIndices.GainsIndices[i] = gainIndices[i]
	}
	if encodeFrameTraceEnabled {
		for i := 0; i < numSubframes && i < maxNbSubfr; i++ {
			if i < len(gainsUnqQ16) {
				traceGainsUnqQ16[i] = gainsUnqQ16[i]
			}
			if i < len(gainsQ16) {
				traceGainsQuantQ16[i] = gainsQ16[i]
			}
		}
	}

	// LBRR uses pre-NSQ indices/NSQ state (libopus silk_LBRR_encode_FLP before the bitrate loop).
	if e.lbrrEnabled {
		e.lbrrEncode(framePCM, frameIndices, lpcQ12, predCoefQ12, interpIdx, pitchLags, ltpCoeffs, ltpScaleIndex, noiseParams, int(seed), numSubframes, subframeSamples, frameSamples, int(speechActivityQ8), currentPrevInd, condCoding)
	}

	ltpScaleQ14 := int32(0)
	if signalType == typeVoiced {
		ltpScaleQ14 = int32(silk_LTPScales_table_Q14[ltpScaleIndex])
	}

	// Bitrate control: multi-pass NSQ + index encoding.
	bitsMargin := 5
	if !blockUseCBR {
		bitsMargin = maxBits / 4
	}

	maxIter := 6
	gainMultQ8 := int16(1 << 8)
	foundLower := false
	foundUpper := false
	gainsID := silkGainsID(gainIndices, numSubframes)
	gainsIDLower := int32(-1)
	gainsIDUpper := int32(-1)

	rangeCopy := *e.rangeEncoder
	nsqCopy0 := *e.nsqState
	seedCopy := frameIndices.Seed
	ecPrevLagIndexCopy := e.ecPrevLagIndex
	ecPrevSignalTypeCopy := e.ecPrevSignalType
	rangeCopy2 := *e.rangeEncoder
	nsqCopy1 := *e.nsqState
	var lastGainIndexCopy2 int8
	ecBufCopy := ensureByteSlice(&e.scratchEcBufCopy, len(e.rangeEncoder.Buffer()))
	var nBits, nBitsLower, nBitsUpper int
	var gainMultLower, gainMultUpper int32
	var gainLock [maxNbSubfr]bool
	var bestGainMult [maxNbSubfr]int16
	var bestSum [maxNbSubfr]int
	var pulses []int8

	for iter := 0; ; iter++ {
		if gainsID == gainsIDLower {
			nBits = nBitsLower
		} else if gainsID == gainsIDUpper {
			nBits = nBitsUpper
		} else {
			if iter > 0 {
				*e.rangeEncoder = rangeCopy
				*e.nsqState = nsqCopy0
				frameIndices.Seed = seedCopy
				e.ecPrevLagIndex = ecPrevLagIndexCopy
				e.ecPrevSignalType = ecPrevSignalTypeCopy
			}

			// Noise shaping quantization
			var seedOut int
			pulses, seedOut = e.computeNSQExcitation(framePCM, lpcQ12, predCoefQ12, interpIdx, gainsQ16, pitchLags, ltpCoeffs, ltpScaleQ14, signalType, quantOffset, int(speechActivityQ8), noiseParams, int(frameIndices.Seed), numSubframes, subframeSamples, frameSamples, e.nsqState)
			frameIndices.Seed = int8(seedOut)
			frameIndices.quantOffsetType = int8(quantOffset)

			if iter == maxIter && !foundLower {
				rangeCopy2 = *e.rangeEncoder
			}

			// Encode indices
			var traceIndex [encodeFrameIndexTracePointCount]encodeFrameRangeTrace
			e.encodeFrameType(vadFlag, signalType, int(frameIndices.quantOffsetType))
			if encodeFrameTraceEnabled {
				traceIndex[encodeFrameIndexTraceAfterType] = encodeFrameRangeTrace{
					tell: e.rangeEncoder.Tell(),
					rng:  e.rangeEncoder.Range(),
				}
			}
			if condCoding == codeConditionally {
				e.rangeEncoder.EncodeICDF(int(frameIndices.GainsIndices[0]), silk_delta_gain_iCDF, 8)
			} else {
				e.encodeAbsoluteGainIndex(int(frameIndices.GainsIndices[0]), signalType)
			}
			for i := 1; i < numSubframes; i++ {
				e.rangeEncoder.EncodeICDF(int(frameIndices.GainsIndices[i]), silk_delta_gain_iCDF, 8)
			}
			if encodeFrameTraceEnabled {
				traceIndex[encodeFrameIndexTraceAfterGains] = encodeFrameRangeTrace{
					tell: e.rangeEncoder.Tell(),
					rng:  e.rangeEncoder.Range(),
				}
			}
			e.encodeLSF(stage1Idx, residuals, interpIdx, e.bandwidth, signalType, numSubframes)
			if encodeFrameTraceEnabled {
				traceIndex[encodeFrameIndexTraceAfterNLSF] = encodeFrameRangeTrace{
					tell: e.rangeEncoder.Tell(),
					rng:  e.rangeEncoder.Range(),
				}
			}
			if signalType == typeVoiced {
				e.encodePitchLagsWithParams(pitchParams, condCoding)
				if encodeFrameTraceEnabled {
					traceIndex[encodeFrameIndexTraceAfterPitch] = encodeFrameRangeTrace{
						tell: e.rangeEncoder.Tell(),
						rng:  e.rangeEncoder.Range(),
					}
				}
				e.encodeLTPCoeffs(perIndex, ltpIndices[:], numSubframes)
				if condCoding == codeIndependently {
					e.rangeEncoder.EncodeICDF(ltpScaleIndex, silk_LTPscale_iCDF, 8)
				}
				if encodeFrameTraceEnabled {
					traceIndex[encodeFrameIndexTraceAfterLTP] = encodeFrameRangeTrace{
						tell: e.rangeEncoder.Tell(),
						rng:  e.rangeEncoder.Range(),
					}
				}
			} else if encodeFrameTraceEnabled {
				traceIndex[encodeFrameIndexTraceAfterPitch] = traceIndex[encodeFrameIndexTraceAfterNLSF]
				traceIndex[encodeFrameIndexTraceAfterLTP] = traceIndex[encodeFrameIndexTraceAfterNLSF]
			}
			e.rangeEncoder.EncodeICDF(int(frameIndices.Seed), silk_uniform4_iCDF, 8)
			if encodeFrameTraceEnabled {
				traceIndex[encodeFrameIndexTraceAfterSeed] = encodeFrameRangeTrace{
					tell: e.rangeEncoder.Tell(),
					rng:  e.rangeEncoder.Range(),
				}
			}
			if encodeFrameTraceEnabled {
				recordEncodeFrameTrace(e, encodeFrameTrace{
					stage:              encodeFrameTraceAfterIndices,
					iter:               iter,
					tell:               e.rangeEncoder.Tell(),
					rng:                e.rangeEncoder.Range(),
					indexTrace:         traceIndex,
					indices:            frameIndices,
					predGainBits:       tracePredGainBits,
					pitchAutoCorr0Bits: tracePitchAutoCorr0Bits,
					pitchResNrgBits:    tracePitchResNrgBits,
					gainsPreQ16:        traceGainsPreQ16,
					resNrgBits:         traceResNrgBits,
					gainsUnqQ16:        traceGainsUnqQ16,
					gainsQuantQ16:      traceGainsQuantQ16,
				})
			}

			// Encode excitation pulses
			e.encodePulses(pulses, signalType, int(frameIndices.quantOffsetType))
			if encodeFrameTraceEnabled {
				tr := encodeFrameTrace{
					stage:              encodeFrameTraceAfterPulses,
					iter:               iter,
					tell:               e.rangeEncoder.Tell(),
					rng:                e.rangeEncoder.Range(),
					indexTrace:         traceIndex,
					indices:            frameIndices,
					predGainBits:       tracePredGainBits,
					pitchAutoCorr0Bits: tracePitchAutoCorr0Bits,
					pitchResNrgBits:    tracePitchResNrgBits,
					gainsPreQ16:        traceGainsPreQ16,
					resNrgBits:         traceResNrgBits,
					gainsUnqQ16:        traceGainsUnqQ16,
					gainsQuantQ16:      traceGainsQuantQ16,
					pulses:             pulses,
				}
				fillCtrlTrace(&tr, signalType, quantOffset, numSubframes, noiseParams, gainsQ16, pitchLags)
				recordEncodeFrameTrace(e, tr)
			}

			nBits = e.rangeEncoder.Tell()

			// If we still bust after the last iteration, do some damage control.
			if iter == maxIter && !foundLower && nBits > maxBits {
				*e.rangeEncoder = rangeCopy2
				for i := 0; i < numSubframes; i++ {
					frameIndices.GainsIndices[i] = 4
				}
				if condCoding != codeConditionally {
					frameIndices.GainsIndices[0] = lastGainIndexPrev
				}
				e.ecPrevLagIndex = ecPrevLagIndexCopy
				e.ecPrevSignalType = ecPrevSignalTypeCopy
				currentPrevInd = lastGainIndexPrev
				for i := range pulses {
					pulses[i] = 0
				}

				e.encodeFrameType(vadFlag, signalType, int(frameIndices.quantOffsetType))
				if condCoding == codeConditionally {
					e.rangeEncoder.EncodeICDF(int(frameIndices.GainsIndices[0]), silk_delta_gain_iCDF, 8)
				} else {
					e.encodeAbsoluteGainIndex(int(frameIndices.GainsIndices[0]), signalType)
				}
				for i := 1; i < numSubframes; i++ {
					e.rangeEncoder.EncodeICDF(int(frameIndices.GainsIndices[i]), silk_delta_gain_iCDF, 8)
				}
				e.encodeLSF(stage1Idx, residuals, interpIdx, e.bandwidth, signalType, numSubframes)
				if signalType == typeVoiced {
					e.encodePitchLagsWithParams(pitchParams, condCoding)
					e.encodeLTPCoeffs(perIndex, ltpIndices[:], numSubframes)
					if condCoding == codeIndependently {
						e.rangeEncoder.EncodeICDF(ltpScaleIndex, silk_LTPscale_iCDF, 8)
					}
				}
				e.rangeEncoder.EncodeICDF(int(frameIndices.Seed), silk_uniform4_iCDF, 8)
				if encodeFrameTraceEnabled {
					recordEncodeFrameTrace(e, encodeFrameTrace{
						stage:   encodeFrameTraceAfterIndices,
						iter:    iter,
						tell:    e.rangeEncoder.Tell(),
						rng:     e.rangeEncoder.Range(),
						indices: frameIndices,
					})
				}
				if pulses != nil {
					for i := range pulses {
						pulses[i] = 0
					}
					e.encodePulses(pulses, signalType, int(frameIndices.quantOffsetType))
				}
				if encodeFrameTraceEnabled {
					recordEncodeFrameTrace(e, encodeFrameTrace{
						stage:   encodeFrameTraceAfterPulses,
						iter:    iter,
						tell:    e.rangeEncoder.Tell(),
						rng:     e.rangeEncoder.Range(),
						indices: frameIndices,
						pulses:  pulses,
					})
				}
				nBits = e.rangeEncoder.Tell()
			}

			if !blockUseCBR && iter == 0 && nBits <= maxBits {
				break
			}
		}

		if iter == maxIter {
			if foundLower && (gainsID == gainsIDLower || nBits > maxBits) {
				*e.rangeEncoder = rangeCopy2
				offs := int(rangeCopy2.Offs())
				if offs <= len(ecBufCopy) {
					copy(e.rangeEncoder.Buffer()[:offs], ecBufCopy[:offs])
				}
				*e.nsqState = nsqCopy1
				currentPrevInd = lastGainIndexCopy2
			}
			break
		}

		if nBits > maxBits {
			if !foundLower && iter >= 2 {
				if noiseParams != nil {
					lambda := max(
						// Match libopus encode_frame_FLP.c:
						// sEncCtrl.Lambda = silk_max_float(sEncCtrl.Lambda*1.5f, 1.5f)
						// (Q10 => minimum 1.5 * 1024 = 1536).
						noiseParams.LambdaQ10+noiseParams.LambdaQ10/2, 1536)
					noiseParams.LambdaQ10 = lambda
				}
				quantOffset = 0
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
				rangeCopy2 = *e.rangeEncoder
				offs := int(rangeCopy2.Offs())
				if offs <= len(ecBufCopy) {
					copy(ecBufCopy[:offs], e.rangeEncoder.Buffer()[:offs])
				}
				nsqCopy1 = *e.nsqState
				lastGainIndexCopy2 = currentPrevInd
			}
		} else {
			break
		}

		if !foundLower && nBits > maxBits && pulses != nil {
			for i := 0; i < numSubframes; i++ {
				sum := 0
				start := i * subframeSamples
				end := min(start+subframeSamples, len(pulses))
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
				next := min(int(gainMultQ8)*3/2, 1024)
				gainMultQ8 = int16(next)
			} else {
				next := max(int(gainMultQ8)*4/5, 64)
				gainMultQ8 = int16(next)
			}
		} else {
			num := (gainMultUpper - gainMultLower) * int32(maxBits-nBitsLower)
			den := nBitsUpper - nBitsLower
			if den == 0 {
				den = 1
			}
			gainMultQ8 = int16(gainMultLower + num/int32(den))
			upper := gainMultLower + ((gainMultUpper - gainMultLower) >> 2)
			lower := gainMultUpper - ((gainMultUpper - gainMultLower) >> 2)
			if int32(gainMultQ8) > upper {
				gainMultQ8 = int16(upper)
			} else if int32(gainMultQ8) < lower {
				gainMultQ8 = int16(lower)
			}
		}

		for i := 0; i < numSubframes; i++ {
			tmp := gainMultQ8
			if gainLock[i] {
				tmp = bestGainMult[i]
			}
			gainsQ16[i] = silk_LSHIFT_SAT32(silk_SMULWB(gainsUnqQ16[i], int32(tmp)), 8)
		}
		currentPrevInd = silkGainsQuantInto(gainIndices, gainsQ16, lastGainIndexPrev, condCoding == codeConditionally, numSubframes)
		for i := 0; i < numSubframes; i++ {
			frameIndices.GainsIndices[i] = gainIndices[i]
		}
		gainsID = silkGainsID(gainIndices, numSubframes)
	}

	e.previousGainIndex = currentPrevInd
	e.previousLogGain = int32(currentPrevInd)
	e.ecPrevSignalType = int32(signalType)
	e.lastQuantOffsetType = int(frameIndices.quantOffsetType)
	e.lastSeed = frameIndices.Seed
	e.isPreviousFrameVoiced = (signalType == typeVoiced)
	copy(e.prevLSFQ15, lsfQ15)

	pitchBufFrameLen := len(framePCM)
	if pitchBufFrameLen > 0 && len(e.pitchAnalysisBuf) > 0 {
		if len(e.pitchAnalysisBuf) > pitchBufFrameLen {
			copy(e.pitchAnalysisBuf, e.pitchAnalysisBuf[pitchBufFrameLen:])
		}
		start := len(e.pitchAnalysisBuf) - pitchBufFrameLen
		if start < 0 {
			start = 0
			pitchBufFrameLen = len(e.pitchAnalysisBuf)
		}
		copy(e.pitchAnalysisBuf[start:], framePCM[:pitchBufFrameLen])
	}

	return e.finalizeEncodeFrame(frameSamples, payloadSizeMs, vadFlag, useSharedEncoder)
}

// finalizeEncodeFrame performs the per-frame buffer shift and the shared
// packet-finalization tail (header patch, byte count, nBitsExceeded, bandwidth
// switch). It is shared between the float analysis path and the FIXED_POINT
// driver so both produce identical packet bookkeeping.
func (e *Encoder) finalizeEncodeFrame(frameSamples, payloadSizeMs int, vadFlag, useSharedEncoder bool) []byte {
	// Shift input buffer at end of frame (matches libopus memmove timing)
	e.shiftInputBuffer(frameSamples)

	e.nFramesEncoded++
	e.MarkEncoded()
	if e.forceFirstFrameAfterReset {
		e.forceFirstFrameAfterReset = false
	}
	e.lastRng = e.rangeEncoder.Range()

	if useSharedEncoder {
		// In libopus, nBitsExceeded is only updated once per packet at the
		// enc_API.c level (lines 555-557), after all frames in the packet are
		// encoded.  The per-frame encode function does NOT update nBitsExceeded.
		// The packet-level caller (EncodePacketWithFEC or hybrid encoder) is
		// responsible for the nBitsExceeded update.
		return nil
	}

	// Patch SILK header bits (VAD + LBRR) at start of bitstream.
	flags := uint32(0)
	if vadFlag {
		flags = 1
	}
	flags = (flags << 1) | uint32(e.lbrrFlag&1)
	e.rangeEncoder.PatchInitialBits(flags, 2)

	// Capture nBytesOut BEFORE ec_enc_done, matching libopus encode_frame_FLP.c:381:
	//   *pnBytesOut = silk_RSHIFT( ec_tell( psRangeEnc ) + 7, 3 );
	// This is the value libopus stores in *pnBytesOut and feeds into the
	// packet-level nBitsExceeded reservoir update (enc_API.c). ec_tell counts the
	// pre-flush bit position, so (ec_tell+7)>>3 can be one byte larger than the
	// buffer ec_enc_done() actually emits; libopus uses this larger pre-flush
	// estimate for the reservoir, NOT the flushed length.
	nBytesOut := max((e.rangeEncoder.Tell()+7)>>3, 0)

	raw := e.rangeEncoder.Done()
	resultLen := min(nBytesOut, len(raw))

	// Match libopus: return exactly ec_tell() byte count for the frame.
	result := raw[:resultLen]

	if e.targetRateBps > 0 && payloadSizeMs > 0 {
		// Match libopus enc_API.c nBitsExceeded update exactly:
		//   psEnc->nBitsExceeded += *nBytesOut * 8;
		//   psEnc->nBitsExceeded -= silk_DIV32_16(silk_MUL(bitRate, payloadSize_ms), 1000);
		//   psEnc->nBitsExceeded = silk_LIMIT(psEnc->nBitsExceeded, 0, 10000);
		// *nBytesOut is the pre-flush (ec_tell+7)>>3 estimate, not the flushed
		// buffer length, so the reservoir tracks libopus even when ec_enc_done
		// emits one fewer byte than the bit position implies.
		e.nBitsExceeded += int32(nBytesOut * 8)
		e.nBitsExceeded -= (e.targetRateBps * int32(payloadSizeMs)) / 1000
		if e.nBitsExceeded < 0 {
			e.nBitsExceeded = 0
		} else if e.nBitsExceeded > 10000 {
			e.nBitsExceeded = 10000
		}
	}
	e.updateAllowBandwidthSwitch(payloadSizeMs)
	e.finishLBRRPacket()
	e.rangeEncoder = nil
	return result
}

func (e *Encoder) updateAllowBandwidthSwitch(payloadSizeMs int) {
	if payloadSizeMs <= 0 {
		return
	}
	thresholdQ8 := silkSMLAWB(
		int32(speechActivityDTXThresholdQ8),
		int32(e.timeSinceSwitchAllowedMS),
		int32(bandwidthSwitchDelaySlopeQ24Q8),
	)
	if e.lastSpeechActivityQ8 < thresholdQ8 {
		e.allowBandwidthSwitch = true
		e.timeSinceSwitchAllowedMS = 0
		return
	}
	e.allowBandwidthSwitch = false
	e.timeSinceSwitchAllowedMS += int32(payloadSizeMs)
	if e.timeSinceSwitchAllowedMS < 0 {
		e.timeSinceSwitchAllowedMS = maxBandwidthSwitchDelayMS
	}
}

// PrefillFrame primes SILK analysis/history buffers without coding a payload.
// This mirrors libopus prefill behavior used on CELT->SILK/HYBRID transitions.
func (e *Encoder) PrefillFrame(pcm []float32) {
	if len(pcm) == 0 {
		return
	}
	config := GetBandwidthConfig(e.bandwidth)
	subframeSamples := config.SubframeSamples
	numSubframes := len(pcm) / subframeSamples
	if numSubframes < 1 {
		numSubframes = 1
		subframeSamples = len(pcm)
	}
	if numSubframes > maxNbSubfr {
		numSubframes = maxNbSubfr
	}
	frameSamples := min(numSubframes*subframeSamples, len(pcm))
	if frameSamples <= 0 {
		return
	}

	pcm = e.quantizePCMToInt16(pcm)

	// Apply LP variable cutoff when active (same as EncodeFrame).
	if e.lpState.Mode != 0 {
		lpBuf := ensureInt16Slice(&e.scratchLPInt16, frameSamples)
		scale := float32(silkSampleScale)
		for i := range frameSamples {
			lpBuf[i] = int16(floatToInt16Round(pcm[i] * scale))
		}
		e.lpState.LPVariableCutoff(lpBuf, frameSamples)
		invScale := float32(1.0 / silkSampleScale)
		for i := range frameSamples {
			pcm[i] = float32(lpBuf[i]) * invScale
		}
	}

	// Match the normal frame-end bookkeeping that libopus still executes during
	// prefill: both the noise-shaping buffer and the pitch-analysis history
	// advance even though no entropy-coded payload is produced.
	_ = e.updateShapeBuffer(pcm, frameSamples)
	pitchBufFrameLen := len(pcm)
	if pitchBufFrameLen > 0 && len(e.pitchAnalysisBuf) > 0 {
		if len(e.pitchAnalysisBuf) > pitchBufFrameLen {
			copy(e.pitchAnalysisBuf, e.pitchAnalysisBuf[pitchBufFrameLen:])
		}
		start := len(e.pitchAnalysisBuf) - pitchBufFrameLen
		if start < 0 {
			start = 0
			pitchBufFrameLen = len(e.pitchAnalysisBuf)
		}
		copy(e.pitchAnalysisBuf[start:], pcm[:pitchBufFrameLen])
	}
	e.shiftInputBuffer(frameSamples)
	e.frameCounter++
}

func (e *Encoder) computeNSQExcitation(pcm []float32, lpcQ12 []int16, predCoefQ12 []int16, nlsfInterpQ2 int, gainsQ16 []int32, pitchLags []int32, ltpCoeffs LTPCoeffsArray, ltpScaleQ14 int32, signalType, quantOffset, speechActivityQ8 int, noiseParams *NoiseShapeParams, seed, numSubframes, subframeSamples, frameSamples int, nsqState *NSQState) ([]int8, int) {
	inputQ0 := ensureInt16Slice(&e.scratchInputQ0, frameSamples)
	for i := 0; i < frameSamples && i < len(pcm); i++ {
		inputQ0[i] = float32ToInt16(pcm[i])
	}
	if len(gainsQ16) < numSubframes {
		tmp := ensureInt32Slice(&e.scratchGainsQ16, numSubframes)
		copy(tmp, gainsQ16)
		for i := len(gainsQ16); i < numSubframes; i++ {
			tmp[i] = 1 << 16
		}
		gainsQ16 = tmp
	}
	pitchL := ensureInt32Slice(&e.scratchPitchL, numSubframes)
	for i := range pitchL {
		pitchL[i] = 0
	}
	if pitchLags != nil {
		copy(pitchL, pitchLags)
	}
	shapeLPCOrder := int(e.shapingLPCOrder)
	if shapeLPCOrder <= 0 {
		shapeLPCOrder = len(lpcQ12)
	}
	if shapeLPCOrder > maxShapeLpcOrder {
		shapeLPCOrder = maxShapeLpcOrder
	}
	if shapeLPCOrder < 2 {
		shapeLPCOrder = 2
	}
	if shapeLPCOrder&1 != 0 {
		shapeLPCOrder--
	}
	var arShpQ13 []int16
	if noiseParams != nil && len(noiseParams.ARShpQ13) >= numSubframes*maxShapeLpcOrder {
		arShpQ13 = noiseParams.ARShpQ13[:numSubframes*maxShapeLpcOrder]
	} else {
		arShpQ13 = ensureInt16Slice(&e.scratchArShpQ13, numSubframes*maxShapeLpcOrder)
		for i := range arShpQ13 {
			arShpQ13[i] = 0
		}
		for sf := range numSubframes {
			for i := 0; i < shapeLPCOrder && i < len(lpcQ12); i++ {
				arShpQ13[sf*maxShapeLpcOrder+i] = int16(int32(lpcQ12[i]) * 2 * 94 / 100)
			}
		}
	}
	ltpCoefQ14 := ensureInt16Slice(&e.scratchLtpCoefQ14, numSubframes*ltpOrderConst)
	for i := range ltpCoefQ14 {
		ltpCoefQ14[i] = 0
	}
	if signalType == typeVoiced {
		for sf := 0; sf < numSubframes && sf < len(ltpCoeffs); sf++ {
			for tap := range ltpOrderConst {
				ltpCoefQ14[sf*ltpOrderConst+tap] = int16(ltpCoeffs[sf][tap]) << 7
			}
		}
	}
	if len(predCoefQ12) < 2*maxLPCOrder {
		predCoefQ12 = ensureInt16Slice(&e.scratchPredCoefQ12, 2*maxLPCOrder)
		for i := range predCoefQ12 {
			predCoefQ12[i] = 0
		}
		for i := 0; i < len(lpcQ12) && i < maxLPCOrder; i++ {
			predCoefQ12[i] = lpcQ12[i]
			predCoefQ12[maxLPCOrder+i] = lpcQ12[i]
		}
		nlsfInterpQ2 = 4
	}
	if noiseParams == nil {
		if e.noiseShapeState == nil {
			e.noiseShapeState = NewNoiseShapeState()
		}
		fsKHz := max(int(e.sampleRate/1000), 8)
		inputQualityBandsQ15 := [4]int32{-1, -1, -1, -1}
		if e.speechActivitySet {
			inputQualityBandsQ15 = e.inputQualityBandsQ15
		}
		// Match libopus: SNR_dB = (silk_float)psEnc->sCmn.SNR_dB_Q7 * ( 1 / 128.0f ) — float32.
		snrDB := float32(e.snrDBQ7) * (1.0 / 128.0)
		noiseParams = e.noiseShapeState.ComputeNoiseShapeParams(signalType, speechActivityQ8, e.ltpCorr, pitchLags, snrDB, quantOffset, inputQualityBandsQ15, numSubframes, fsKHz, int(e.nStatesDelayedDecision))
	}
	harmShapeGainQ14 := ensureInt32Slice(&e.scratchHarmShapeGainQ14, numSubframes)
	tiltQ14 := ensureInt32Slice(&e.scratchTiltQ14, numSubframes)
	lfShpQ14 := ensureInt32Slice(&e.scratchLfShpQ14, numSubframes)
	copy(harmShapeGainQ14, noiseParams.HarmShapeGainQ14)
	copy(tiltQ14, noiseParams.TiltQ14)
	copy(lfShpQ14, noiseParams.LFShpQ14)
	lambdaQ10 := noiseParams.LambdaQ10
	if signalType != typeVoiced {
		ltpScaleQ14 = 0
	}
	ltpMemLengthSamples := ltpMemLengthMs * int(e.sampleRate/1000)
	params := &NSQParams{
		SignalType:             signalType,
		QuantOffsetType:        quantOffset,
		PredCoefQ12:            predCoefQ12,
		NLSFInterpCoefQ2:       nlsfInterpQ2,
		LTPCoefQ14:             ltpCoefQ14,
		ARShpQ13:               arShpQ13,
		HarmShapeGainQ14:       harmShapeGainQ14,
		TiltQ14:                tiltQ14,
		LFShpQ14:               lfShpQ14,
		GainsQ16:               gainsQ16,
		PitchL:                 pitchL,
		LambdaQ10:              lambdaQ10,
		LTPScaleQ14:            ltpScaleQ14,
		FrameLength:            frameSamples,
		SubfrLength:            subframeSamples,
		NbSubfr:                numSubframes,
		LTPMemLength:           ltpMemLengthSamples,
		PredLPCOrder:           len(lpcQ12),
		ShapeLPCOrder:          shapeLPCOrder,
		WarpingQ16:             int(e.warpingQ16),
		NStatesDelayedDecision: int(e.nStatesDelayedDecision),
		Seed:                   seed,
	}
	state := nsqState
	if state == nil {
		state = e.nsqState
	}
	seedOut := seed
	var pulses []int8
	if params.NStatesDelayedDecision > 1 || params.WarpingQ16 > 0 {
		pulses, _, seedOut = NoiseShapeQuantizeDelDec(state, inputQ0, params)
	} else {
		pulses, _ = NoiseShapeQuantize(state, inputQ0, params)
	}
	return pulses, seedOut
}

// EncodePacketWithFEC encodes one SILK packet (up to maxFramesPerPacket 20 ms
// frames) from pcm, prepending in-band FEC (LBRR) for the previous frame.
// lookahead supplies the analysis tail and vadFlags the per-frame voice-activity
// decisions. It is shorthand for EncodePacketWithFECWithVADStates with no
// explicit VAD states.
func (e *Encoder) EncodePacketWithFEC(pcm []float32, lookahead []float32, vadFlags []bool) []byte {
	return e.EncodePacketWithFECWithVADStates(pcm, lookahead, vadFlags, nil)
}

// EncodePacketWithFECWithVADStates is EncodePacketWithFEC with externally
// supplied per-frame VAD states; when vadStates is nil the encoder derives the
// VAD decisions from vadFlags and its own analysis.
func (e *Encoder) EncodePacketWithFECWithVADStates(pcm []float32, lookahead []float32, vadFlags []bool, vadStates []VADFrameState) []byte {
	e.ResetPacketState()
	config := GetBandwidthConfig(e.bandwidth)
	frameSamples := min(len(pcm), config.SampleRate*20/1000)
	nFrames := min(max(len(pcm)/frameSamples, 1), maxFramesPerPacket)
	e.nFramesPerPacket = int32(nFrames)
	bufSize := max(max(len(pcm)/2+100, 150), maxSilkPacketBytes)
	output := ensureByteSlice(&e.scratchOutput, bufSize)
	e.scratchRangeEncoder.Init(output)
	e.rangeEncoder = &e.scratchRangeEncoder
	e.encodeLBRRData(e.rangeEncoder, 1, true)
	baseMaxBits := e.maxBits
	baseUseCBR := e.useCBR
	baseBlockUseCBR := e.blockUseCBR
	var vadUsed [maxFramesPerPacket]int
	for i := 0; i < nFrames; i++ {
		// Match libopus enc_API.c per-block maxBits/useCBR handling for
		// 40/60 ms packets in CBR mode.
		frameMaxBits := baseMaxBits
		switch nFrames {
		case 2:
			if i == 0 {
				frameMaxBits = frameMaxBits * 3 / 5
			}
		case 3:
			if i == 0 {
				frameMaxBits = frameMaxBits * 2 / 5
			} else if i == 1 {
				frameMaxBits = frameMaxBits * 3 / 4
			}
		}
		if frameMaxBits > 0 {
			e.maxBits = frameMaxBits
		}
		e.blockUseCBR = baseUseCBR
		if baseUseCBR && nFrames > 1 {
			e.blockUseCBR = i == nFrames-1
		}

		startSample := i * frameSamples
		endSample := min(startSample+frameSamples, len(pcm))
		framePCM := pcm[startSample:endSample]
		vadFlag := true
		if vadFlags != nil && i < len(vadFlags) {
			vadFlag = vadFlags[i]
		}
		if vadFlag {
			vadUsed[i] = 1
		} else {
			vadUsed[i] = 0
		}
		if i < len(vadStates) && vadStates[i].Valid {
			state := vadStates[i]
			e.SetVADState(state.SpeechActivityQ8, state.InputTiltQ15, state.InputQualityBandsQ15)
		}
		// For multi-frame packets, libopus feeds contiguous input through the
		// internal buffers frame by frame. Only the final frame needs external
		// lookahead from outside this packet.
		var frameLookahead []float32
		if i == nFrames-1 {
			frameLookahead = lookahead
		}
		_ = e.EncodeFrame(framePCM, frameLookahead, vadFlag)
	}
	e.maxBits = baseMaxBits
	e.useCBR = baseUseCBR
	e.blockUseCBR = baseBlockUseCBR
	// Patch SILK header bits (VAD + LBRR) at start of bitstream.
	flags := uint32(0)
	for i := 0; i < nFrames; i++ {
		flags <<= 1
		flags |= uint32(vadUsed[i] & 1)
	}
	flags = (flags << 1) | uint32(e.lbrrFlag&1)
	e.rangeEncoder.PatchInitialBits(flags, uint(nFrames+1))
	e.lastRng = e.rangeEncoder.Range()
	// Capture nBytesOut BEFORE ec_enc_done, matching libopus encode_frame_FLP.c:381.
	// As in the single-frame path, this pre-flush (ec_tell+7)>>3 estimate is what
	// libopus feeds into the reservoir; it can exceed the flushed buffer length.
	nBytesOut := max((e.rangeEncoder.Tell()+7)>>3, 0)
	raw := e.rangeEncoder.Done()
	resultLen := min(nBytesOut, len(raw))
	result := raw[:resultLen]
	if e.targetRateBps > 0 {
		payloadSizeMs := (nFrames * frameSamples * 1000) / config.SampleRate
		if payloadSizeMs > 0 {
			e.nBitsExceeded += int32(nBytesOut * 8)
			e.nBitsExceeded -= (e.targetRateBps * int32(payloadSizeMs)) / 1000
			if e.nBitsExceeded < 0 {
				e.nBitsExceeded = 0
			} else if e.nBitsExceeded > 10000 {
				e.nBitsExceeded = 10000
			}
		}
	}
	payloadSizeMs := (nFrames * frameSamples * 1000) / config.SampleRate
	e.updateAllowBandwidthSwitch(payloadSizeMs)
	e.finishLBRRPacket()
	e.rangeEncoder = nil
	return result
}

func (e *Encoder) encodeFrameType(vadFlag bool, signalType, quantOffset int) {
	typeOffset := 2*signalType + quantOffset
	if vadFlag {
		sym := max(typeOffset-2, 0)
		if sym >= len(silk_type_offset_VAD_iCDF) {
			sym = len(silk_type_offset_VAD_iCDF) - 1
		}
		e.rangeEncoder.EncodeICDF(sym, silk_type_offset_VAD_iCDF, 8)
		return
	}
	// VAD inactive uses a dedicated 2-symbol table (typeOffset 0 or 1).
	sym := max(typeOffset, 0)
	if sym >= len(silk_type_offset_no_VAD_iCDF) {
		sym = len(silk_type_offset_no_VAD_iCDF) - 1
	}
	e.rangeEncoder.EncodeICDF(sym, silk_type_offset_no_VAD_iCDF, 8)
}

func (e *Encoder) quantizePCMToInt16(pcm []float32) []float32 {
	if len(pcm) == 0 {
		return pcm
	}
	quantized := ensureFloat32Slice(&e.scratchPCMQuant, len(pcm))
	scale := float32(silkSampleScale)
	invScale := float32(1.0 / silkSampleScale)
	for i, v := range pcm {
		quantized[i] = float32(floatToInt16Round(v*scale)) * invScale
	}
	return quantized
}

func (e *Encoder) updateShapeBuffer(pcm []float32, frameSamples int) []float32 {
	if frameSamples <= 0 {
		return pcm
	}
	fsKHz := max(int(e.sampleRate/1000), 1)
	ltpMemSamples := ltpMemLengthMs * fsKHz
	laShapeSamples := laShapeMs * fsKHz
	keep := ltpMemSamples + laShapeSamples
	needed := keep + frameSamples
	shapeBuf := e.inputBuffer
	if len(shapeBuf) < needed {
		shapeBuf = make([]float32, needed)
		e.inputBuffer = shapeBuf
	}

	// Per libopus silk/float/encode_frame_FLP.c:
	// 1. Copy new PCM to buffer FIRST (before shift)
	// 2. Run pitch analysis
	// 3. Shift buffer at frame END (after encoding)
	//
	// This ensures LTP memory contains the correct history at pitch analysis time.
	// The shift for frame N happens AFTER frame N encoding (in shiftInputBuffer).

	insertOffset := keep
	if insertOffset+frameSamples > len(shapeBuf) {
		if insertOffset >= len(shapeBuf) {
			return pcm
		}
		frameSamples = len(shapeBuf) - insertOffset
	}
	insert := shapeBuf[insertOffset : insertOffset+frameSamples]
	n := copy(insert, pcm)
	for i := n; i < frameSamples; i++ {
		insert[i] = 0
	}
	// Match libopus encode_frame_FLP: add a tiny anti-denormal signal to
	// eight evenly spaced positions in the freshly copied frame.
	// This is applied to x_buf after short->float conversion.
	step := frameSamples >> 3
	if step > 0 {
		// Our x_buf is normalized to [-1, 1], while libopus x_buf is int16-scaled.
		// Scale the tiny anti-denormal term to match libopus magnitude.
		antiDenormal := float32(1e-6 / silkSampleScale)
		for i := range 8 {
			idx := i * step
			if idx >= 0 && idx < frameSamples {
				dither := antiDenormal
				if (i & 2) != 0 {
					dither = -dither
				}
				insert[idx] += dither
			}
		}
	}
	start := ltpMemSamples
	if start+frameSamples > len(shapeBuf) {
		if start >= len(shapeBuf) {
			return pcm
		}
		frameSamples = len(shapeBuf) - start
	}
	return shapeBuf[start : start+frameSamples]
}

// shiftInputBuffer shifts the input buffer left by frameSamples after frame encoding.
// This matches libopus silk/float/encode_frame_FLP.c silk_memmove at end of frame.
// Must be called AFTER encoding is complete for the current frame.
func (e *Encoder) shiftInputBuffer(frameSamples int) {
	if frameSamples <= 0 {
		return
	}
	fsKHz := max(int(e.sampleRate/1000), 1)
	ltpMemSamples := ltpMemLengthMs * fsKHz
	laShapeSamples := laShapeMs * fsKHz
	keep := ltpMemSamples + laShapeSamples
	shapeBuf := e.inputBuffer
	if len(shapeBuf) < keep+frameSamples {
		return
	}
	// Per libopus: memmove(x_buf, x_buf + frame_length, ltp_mem + la_shape)
	// This shifts buffer left by frame_length, keeping ltp_mem + la_shape samples.
	copy(shapeBuf[:keep], shapeBuf[frameSamples:frameSamples+keep])
}

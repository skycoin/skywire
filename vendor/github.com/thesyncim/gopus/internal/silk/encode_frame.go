package silk

import (
	"math"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	speechActivityDTXThresholdQ8   = 13   // SILK_FIX_CONST(0.05, 8)
	bandwidthSwitchDelaySlopeQ24Q8 = 3188 // SILK_FIX_CONST((1 - 0.05) / 5000, 24)
	nbSpeechFramesBeforeDTX        = 10   // NB_SPEECH_FRAMES_BEFORE_DTX (200 ms)
	maxConsecutiveDTX              = 20   // MAX_CONSECUTIVE_DTX (400 ms)
)

// encodeFrame is silk_encode_frame_FLP (silk/float/encode_frame_FLP.c): it
// codes the frame held in inputBuf[1:] into re with the given conditional
// coding, bit cap and CBR flag, and returns the payload size so far in bytes,
// (ec_tell+7)>>3. The VAD decision for the frame (silk_encode_do_VAD_FLP) and
// the target SNR (silk_control_SNR) are set before the call. During a prefill
// it only runs the LP filter and advances the analysis buffers, and returns 0.
func (e *Encoder) encodeFrame(re *rangecoding.Encoder, condCoding int, maxBits int, useCBR bool) int32 {
	config := GetBandwidthConfig(e.bandwidth)
	numSubframes := int(e.nbSubfr)
	subframeSamples := config.SubframeSamples
	frameSamples := int(e.frameLength)
	vadFlag := e.vadFlags[e.nFramesEncoded]
	firstFrameAfterReset := e.firstFrameAfterReset

	// Ensure smooth bandwidth transitions (silk_LP_variable_cutoff on
	// inputBuf+1, in place).
	in := e.inputBuf[1 : 1+frameSamples]
	if e.lpState.Mode != 0 {
		e.lpState.LPVariableCutoff(in, frameSamples)
	}

	if e.prefillFlag {
		if e.fixedEncodeActive() {
			e.prefillFrameFixed(in)
		} else {
			e.prefillFrame(in)
		}
		return 0
	}
	e.rangeEncoder = re

	// Under the gopus_fixed_point build the SILK analysis + rate-control body is
	// driven by the bit-exact FIXED_POINT chain instead of the float analysis.
	if e.fixedEncodeActive() {
		return e.encodeFrameFixedBody(in, numSubframes, subframeSamples, condCoding, vadFlag, firstFrameAfterReset, maxBits, useCBR)
	}

	pcm := ensureFloat32Slice(&e.frameF32, frameSamples)
	for i, v := range in {
		pcm[i] = float32(v) * (1.0 / silkSampleScale)
	}

	// Step 1: the VAD decision sets the initial signal type.
	signalType := typeNoVoiceActivity
	if vadFlag {
		signalType = typeUnvoiced
	}
	quantOffset := 0
	speechActivityQ8 := e.speechActivityQ8

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
			pitchLags = ensureInt32Slice(&e.scratchPitchLags, numSubframes)
			clear(pitchLags)
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
	pitchBuf := e.xBuf
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
		noiseParams.Lambda = computeLambda(signalType, int(speechActivityQ8), quantOffset, int(e.nStatesDelayedDecision), noiseParams.CodingQuality, noiseParams.InputQuality)
	}

	// Step 7: Prepare indices and gains for bitrate control loop.
	// Match libopus silk_encode_frame_{FLP,FIX}: the frame counter advances
	// when the entropy seed is selected, before the prefill/encode branch exits.
	seed := e.frameCounter & 3
	e.frameCounter++
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

	// Bitrate control: multi-pass NSQ + index encoding. For CBR, 5 bits below
	// budget is close enough; for VBR, allow up to 25% below the cap.
	bitsMargin := 5
	if !useCBR {
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
	var lastGainIndexCopy2 int8
	ecBufCopy := ensureByteSlice(&e.scratchEcBufCopy, len(e.rangeEncoder.Buffer()))
	var nBits, nBitsLower, nBitsUpper int
	var gainMultLower, gainMultUpper int32
	var gainLock [maxNbSubfr]bool
	var bestGainMult [maxNbSubfr]int16
	var bestSum [maxNbSubfr]int
	var pulses []int8

gainSearch:
	for iter := 0; ; iter++ {
		switch gainsID {
		case gainsIDLower:
			nBits = nBitsLower
		case gainsIDUpper:
			nBits = nBitsUpper
		default:
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
			if encodeFrameTraceEnabled {
				recordEncodeFrameTrace(e, encodeFrameTrace{
					stage:   encodeFrameTraceAfterNSQ,
					iter:    iter,
					tell:    e.rangeEncoder.Tell(),
					rng:     e.rangeEncoder.Range(),
					indices: frameIndices,
					pulses:  pulses,
				})
			}

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

			if !useCBR && iter == 0 && nBits <= maxBits {
				break gainSearch
			}
		}

		if iter == maxIter {
			if foundLower && (gainsID == gainsIDLower || nBits > maxBits) {
				*e.rangeEncoder = rangeCopy2
				offs := int(rangeCopy2.Offs())
				if offs <= len(ecBufCopy) {
					copy(e.rangeEncoder.Buffer()[:offs], ecBufCopy[:offs])
				}
				*e.nsqState = e.nsqLowerCopy
				currentPrevInd = lastGainIndexCopy2
			}
			break
		}

		if nBits > maxBits {
			if !foundLower && iter >= 2 {
				if noiseParams != nil {
					// Adjust the quantizer's rate/distortion tradeoff
					// (silk/float/encode_frame_FLP.c).
					noiseParams.Lambda = max(noiseParams.Lambda*1.5, 1.5)
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
				e.nsqLowerCopy = *e.nsqState
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

		if !foundLower || !foundUpper {
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
	e.ecPrevSignalType = int32(signalType)
	e.lastQuantOffsetType = int(frameIndices.quantOffsetType)
	e.lastSeed = frameIndices.Seed
	e.isPreviousFrameVoiced = (signalType == typeVoiced)
	copy(e.prevLSFQ15[:], lsfQ15)

	return e.finishFrame(frameSamples)
}

// finishFrame ends a coded frame, shared by the float analysis path and the
// FIXED_POINT driver: it shifts x_buf, clears first_frame_after_reset and
// returns the payload size so far, silk_RSHIFT(ec_tell + 7, 3).
func (e *Encoder) finishFrame(frameSamples int) int32 {
	e.shiftInputBuffer(frameSamples)
	e.firstFrameAfterReset = false
	return int32((e.rangeEncoder.Tell() + 7) >> 3)
}

// prefillFrame is the prefill branch of silk_encode_frame_FLP: the LP-filtered
// frame enters x_buf and the analysis history advances, without analysis or
// entropy coding.
func (e *Encoder) prefillFrame(in []int16) {
	frameSamples := len(in)
	pcm := ensureFloat32Slice(&e.frameF32, frameSamples)
	for i, v := range in {
		pcm[i] = float32(v) * (1.0 / silkSampleScale)
	}
	_ = e.updateShapeBuffer(pcm, frameSamples)
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
		fsKHz := max(int(e.fsKHz), 8)
		inputQualityBandsQ15 := e.inputQualityBandsQ15
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
	lambdaQ10 := float32ToInt32RoundEven(noiseParams.Lambda * 1024)
	if signalType != typeVoiced {
		ltpScaleQ14 = 0
	}
	ltpMemLengthSamples := ltpMemLengthMs * int(e.fsKHz)
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

func (e *Encoder) updateShapeBuffer(pcm []float32, frameSamples int) []float32 {
	if frameSamples <= 0 {
		return pcm
	}
	fsKHz := max(int(e.fsKHz), 1)
	ltpMemSamples := ltpMemLengthMs * fsKHz
	laShapeSamples := laShapeMs * fsKHz
	keep := ltpMemSamples + laShapeSamples
	needed := keep + frameSamples
	shapeBuf := e.xBuf
	if len(shapeBuf) < needed {
		shapeBuf = make([]float32, needed)
		e.xBuf = shapeBuf
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
	fsKHz := max(int(e.fsKHz), 1)
	ltpMemSamples := ltpMemLengthMs * fsKHz
	laShapeSamples := laShapeMs * fsKHz
	keep := ltpMemSamples + laShapeSamples
	shapeBuf := e.xBuf
	if len(shapeBuf) < keep+frameSamples {
		return
	}
	// Per libopus: memmove(x_buf, x_buf + frame_length, ltp_mem + la_shape)
	// This shifts buffer left by frame_length, keeping ltp_mem + la_shape samples.
	copy(shapeBuf[:keep], shapeBuf[frameSamples:frameSamples+keep])
}

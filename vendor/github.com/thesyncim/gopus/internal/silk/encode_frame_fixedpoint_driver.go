//go:build gopus_fixed_point

package silk

// This file wires the bit-exact FIXED_POINT SILK per-frame driver
// (silkEncodeFramePayloadFIX) into the public silk.Encoder so that
// Encoder.EncodeFrame produces byte-exact SILK payloads matching the libopus
// FIXED_POINT encoder (silk/fixed/encode_frame_FIX.c + silk/enc_API.c). The
// surrounding orchestration (range-coder init, LBRR header emit, multi-frame
// loop, VAD/FEC header patch) is shared with the float path in encode_frame.go;
// only the per-frame analysis + rate-control body is replaced here.
//
// The driver maintains its own integer cross-frame state (int16 x_buf, VAD
// state, noise-shape smoothers, previous-NLSF history, NSQ state, ...) mirroring
// silk_encoder_state_FIX, fed from the int16-quantized input frame. This avoids
// any dependency on the float analysis history.

// silkEncoderFixedFields carries the FIXED_POINT integer SILK encode state held
// on the public Encoder under the gopus_fixed_point build.
type silkEncoderFixedFields struct {
	fixed *silkFixedEncodeState

	// fixedStereoInt16In, when non-nil, holds the raw int16 mid/side frame
	// (post stereo_LR_to_MS, pre LP_variable_cutoff) that the integer SILK
	// encode body must consume verbatim for one EncodeFrame call. It lets the
	// validated integer stereo front-end (silkStereoLRToMS) feed exact int16
	// mid/side samples into the per-channel encode without a float round-trip,
	// matching libopus enc_API.c where silk_stereo_LR_to_MS writes inputBuf+2
	// and silk_encode_frame_FIX consumes inputBuf+1 directly. It is consumed
	// (cleared) by buildFixedInputBuf.
	fixedStereoInt16In []int16

	// Scratch buffers for the integer stereo front-end (stereoFixedFrontEnd).
	scratchStereoFixedMid     []int16
	scratchStereoFixedSide    []int16
	scratchStereoFixedX2      []int16
	scratchStereoFixedMidOut  []int16
	scratchStereoFixedSideOut []int16
}

// silkFixedEncodeState holds the persistent silk_encoder_state_FIX-equivalent
// state for the integer SILK encode path.
type silkFixedEncodeState struct {
	initialized bool

	// scratch holds the reusable per-frame working buffers for the integer
	// encode path, grown once and reused across every frame of a packet.
	scratch *silkFixedEncodeScratch

	// fs_kHz this state was configured for; a change forces re-init.
	fsKHz int

	// Integer x_buf history (ltp_mem_length + la_shape + frame_length samples),
	// matching silk_encoder_state_FIX.x_buf. The new frame is inserted at
	// x_buf[ltp_mem_length + la_shape].
	xBuf []int16

	// Persistent VAD analysis state (silk_VAD_state).
	vad silkVADState

	// Persistent NSQ state (silk_nsq_state).
	nsq NSQState

	// Mutable common-state carried across frames.
	frameCounter         int32
	prevSignalType       int32
	prevLag              int32
	noSpeechCounter      int32
	inDTX                int32
	firstFrameAfterReset bool
	ltpCorrQ15           int32
	sumLogGainQ7         int32
	prevNLSFqQ15         [maxLPCOrder]int16
	lastGainIndex        int8

	// Mutable shape-state smoothers.
	harmShapeGainSmthQ16 int32
	tiltSmthQ16          int32

	// Conditional-pitch coding carry (sCmn.ec_prev*).
	ecPrevLagIndex   int16
	ecPrevSignalType int32

	// LBRR carry (sCmn.LBRRprevLastGainIndex).
	lbrrPrevLastGainIndex int8

	// VAD-derived state for the current frame (set by silkEncodeDoVADFIX).
	speechActivityQ8     int32
	inputTiltQ15         int32
	inputQualityBandsQ15 [vadNBands]int32

	// lastFixedVADFlag is the integer VAD decision of the most recent encode
	// body call (1 == active), exposed via FixedLastVADFlag for the stereo
	// VAD header patch.
	lastFixedVADFlag bool

	// captureSnapshot enables the per-frame test snapshot capture below. It is
	// off in production so the hot path does not copy x_buf each frame.
	captureSnapshot bool

	// Test-only snapshots captured per frame at the moment the payload driver
	// ran (post-insert, pre-shift), so a parity test can replay them against the
	// libopus silk_encode_frame_FIX oracle.
	testPreEncodeXBuf      []int16
	testPreEncodeInputBuf  []int16
	testPreFrameCounter    int32
	testPrevSignalType     int32
	testPrevLag            int32
	testFirstFrameAfterRst bool
	testLastGainIndex      int8
	testHarmSmthQ16        int32
	testTiltSmthQ16        int32
	testSumLogGainQ7       int32
	testPrevNLSFqQ15       [maxLPCOrder]int16
	testLtpCorrQ15         int32
	testNbSubfr            int
	testFrameLength        int
	testSnrDBQ7            int32
	testMaxBits            int
	testCondCoding         int32
	testPredictLPCOrder    int
	testPitchEstLPCOrder   int
	testShapingLPCOrder    int
	testShapeWinLength     int
	testComplexity         int
	testNStatesDelDec      int
	testWarpingQ16         int32
	testNlsfSurvivors      int
	testPitchEstThrQ16     int32
	testUseCBR             int

	// allSnapshots accumulates one FixedPreEncodeSnapshot per encode-body call
	// (in order) when captureSnapshot is on, so the stereo parity test can replay
	// every mid/side frame against the libopus FIXED_POINT per-frame oracle.
	allSnapshots []FixedPreEncodeSnapshot
}

// fixedEncodeActive reports whether the integer SILK encode path is selected.
// Under the gopus_fixed_point build it is always on for the single-stream SILK
// encoder.
func (e *Encoder) fixedEncodeActive() bool { return true }

// silkFixedEncodeBuild reports whether the integer SILK encode path is compiled
// in. Tests that assert FLOAT-encode-specific behavior (e.g. injected VAD state
// changing the bitstream, or float-calibrated self-decode RMS) gate on it.
const silkFixedEncodeBuild = true

// captureFixedSnapshot records the per-frame inputs the validated payload driver
// is about to consume, for parity tests. Not called in production.
func (e *Encoder) captureFixedSnapshot(
	st *silkFixedEncodeState,
	ps *silkEncodeFramePayloadFIXState,
	inputBuf []int16,
	numSubframes, frameSamples, condCoding, predictLPCOrder, pitchEstLPCOrder, useCBRInt int,
) {
	st.testPreEncodeXBuf = append(st.testPreEncodeXBuf[:0], st.xBuf...)
	st.testPreEncodeInputBuf = append(st.testPreEncodeInputBuf[:0], inputBuf...)
	st.testPreFrameCounter = ps.frameCounter
	st.testPrevSignalType = ps.prevSignalType
	st.testPrevLag = ps.prevLag
	st.testFirstFrameAfterRst = ps.firstFrameAfterReset
	st.testLastGainIndex = ps.lastGainIndex
	st.testHarmSmthQ16 = ps.harmShapeGainSmthQ16
	st.testTiltSmthQ16 = ps.tiltSmthQ16
	st.testSumLogGainQ7 = ps.sumLogGainQ7
	st.testPrevNLSFqQ15 = ps.prevNLSFqQ15
	st.testLtpCorrQ15 = ps.ltpCorrQ15
	st.testNbSubfr = numSubframes
	st.testFrameLength = frameSamples
	st.testSnrDBQ7 = e.snrDBQ7
	st.testMaxBits = int(e.maxBits)
	st.testCondCoding = int32(condCoding)
	st.testPredictLPCOrder = predictLPCOrder
	st.testPitchEstLPCOrder = pitchEstLPCOrder
	st.testShapingLPCOrder = int(e.shapingLPCOrder)
	st.testShapeWinLength = int(e.shapeWinLength)
	st.testComplexity = int(e.pitchEstimationComplexity)
	st.testNStatesDelDec = int(e.nStatesDelayedDecision)
	st.testWarpingQ16 = e.warpingQ16
	st.testNlsfSurvivors = int(e.nlsfSurvivors)
	st.testPitchEstThrQ16 = e.pitchEstimationThresholdQ16
	st.testUseCBR = useCBRInt

	// Also append a self-contained copy to the per-frame snapshot list so the
	// stereo parity test can replay every mid/side frame in order. The slices
	// are copied because the underlying buffers are reused across frames.
	snap := FixedPreEncodeSnapshot{
		XBuf:                 append([]int16(nil), st.xBuf...),
		InputBuf:             append([]int16(nil), inputBuf...),
		FrameCounter:         ps.frameCounter,
		PrevSignalType:       ps.prevSignalType,
		PrevLag:              ps.prevLag,
		FirstFrameAfterReset: ps.firstFrameAfterReset,
		LastGainIndex:        ps.lastGainIndex,
		HarmShapeGainSmthQ16: ps.harmShapeGainSmthQ16,
		TiltSmthQ16:          ps.tiltSmthQ16,
		SumLogGainQ7:         ps.sumLogGainQ7,
		PrevNLSFqQ15:         ps.prevNLSFqQ15,
		LtpCorrQ15:           ps.ltpCorrQ15,
		NbSubfr:              numSubframes,
		FrameLength:          frameSamples,
		SnrDBQ7:              e.snrDBQ7,
		MaxBits:              int(e.maxBits),
		CondCoding:           int32(condCoding),
		PredictLPCOrder:      predictLPCOrder,
		PitchEstLPCOrder:     pitchEstLPCOrder,
		ShapingLPCOrder:      int(e.shapingLPCOrder),
		ShapeWinLength:       int(e.shapeWinLength),
		Complexity:           int(e.pitchEstimationComplexity),
		NStatesDelDec:        int(e.nStatesDelayedDecision),
		WarpingQ16:           e.warpingQ16,
		NlsfSurvivors:        int(e.nlsfSurvivors),
		PitchEstThrQ16:       e.pitchEstimationThresholdQ16,
		UseCBR:               useCBRInt,
	}
	st.allSnapshots = append(st.allSnapshots, snap)
}

// FixedAllSnapshotsForTest returns the per-encode-body snapshots captured for
// this encoder, in call order (one per mid/side frame). For parity tests only.
func (e *Encoder) FixedAllSnapshotsForTest() []FixedPreEncodeSnapshot {
	if e.fixed == nil {
		return nil
	}
	return e.fixed.allSnapshots
}

// EnableFixedSnapshotForTest turns on per-frame snapshot capture for the
// integer SILK encode path. For parity tests only.
func (e *Encoder) EnableFixedSnapshotForTest() {
	st := e.ensureFixedState()
	st.captureSnapshot = true
}

// FixedXBufForTest returns the integer x_buf history maintained by the FIXED
// encode path. For parity tests only.
func (e *Encoder) FixedXBufForTest() []int16 {
	if e.fixed == nil {
		return nil
	}
	return e.fixed.xBuf
}

// FixedFrameCounterForTest returns the integer frame counter. For parity tests.
func (e *Encoder) FixedFrameCounterForTest() int32 {
	if e.fixed == nil {
		return 0
	}
	return e.fixed.frameCounter
}

// FixedPreEncodeSnapshot exposes the per-frame inputs the validated payload
// driver consumed (post buffer-insert, pre-shift) plus the pre-encode state, so
// a parity test can replay them against the libopus silk_encode_frame_FIX
// oracle. For parity tests only.
type FixedPreEncodeSnapshot struct {
	XBuf                 []int16
	InputBuf             []int16
	FrameCounter         int32
	PrevSignalType       int32
	PrevLag              int32
	FirstFrameAfterReset bool
	LastGainIndex        int8
	HarmShapeGainSmthQ16 int32
	TiltSmthQ16          int32
	SumLogGainQ7         int32
	PrevNLSFqQ15         [maxLPCOrder]int16
	LtpCorrQ15           int32
	NbSubfr              int
	FrameLength          int
	SnrDBQ7              int32
	MaxBits              int
	CondCoding           int32
	PredictLPCOrder      int
	PitchEstLPCOrder     int
	ShapingLPCOrder      int
	ShapeWinLength       int
	Complexity           int
	NStatesDelDec        int
	WarpingQ16           int32
	NlsfSurvivors        int
	PitchEstThrQ16       int32
	UseCBR               int
}

// FixedPreEncodeForTest returns the most recent pre-encode snapshot.
func (e *Encoder) FixedPreEncodeForTest() FixedPreEncodeSnapshot {
	st := e.fixed
	if st == nil {
		return FixedPreEncodeSnapshot{}
	}
	return FixedPreEncodeSnapshot{
		XBuf:                 st.testPreEncodeXBuf,
		InputBuf:             st.testPreEncodeInputBuf,
		FrameCounter:         st.testPreFrameCounter,
		PrevSignalType:       st.testPrevSignalType,
		PrevLag:              st.testPrevLag,
		FirstFrameAfterReset: st.testFirstFrameAfterRst,
		LastGainIndex:        st.testLastGainIndex,
		HarmShapeGainSmthQ16: st.testHarmSmthQ16,
		TiltSmthQ16:          st.testTiltSmthQ16,
		SumLogGainQ7:         st.testSumLogGainQ7,
		PrevNLSFqQ15:         st.testPrevNLSFqQ15,
		LtpCorrQ15:           st.testLtpCorrQ15,
		NbSubfr:              st.testNbSubfr,
		FrameLength:          st.testFrameLength,
		SnrDBQ7:              st.testSnrDBQ7,
		MaxBits:              st.testMaxBits,
		CondCoding:           st.testCondCoding,
		PredictLPCOrder:      st.testPredictLPCOrder,
		PitchEstLPCOrder:     st.testPitchEstLPCOrder,
		ShapingLPCOrder:      st.testShapingLPCOrder,
		ShapeWinLength:       st.testShapeWinLength,
		Complexity:           st.testComplexity,
		NStatesDelDec:        st.testNStatesDelDec,
		WarpingQ16:           st.testWarpingQ16,
		NlsfSurvivors:        st.testNlsfSurvivors,
		PitchEstThrQ16:       st.testPitchEstThrQ16,
		UseCBR:               st.testUseCBR,
	}
}

// ensureFixedState lazily allocates / re-initializes the integer SILK state for
// the encoder's current fs_kHz. It mirrors the parts of silk_init_encoder /
// silk_control_encoder first-frame reset that establish the integer cross-frame
// state (sNSQ.prev_gain_Q16, lagPrev, LastGainIndex, prevLag).
func (e *Encoder) ensureFixedState() *silkFixedEncodeState {
	fsKHz := int(e.sampleRate / 1000)
	st := e.fixed
	if st == nil {
		st = &silkFixedEncodeState{}
		e.fixed = st
	}
	if !st.initialized || st.fsKHz != fsKHz {
		st.fsKHz = fsKHz
		ltpMemLength := ltpMemLengthMs * fsKHz
		laShape := laShapeMs * fsKHz
		frameLength := 20 * fsKHz
		st.xBuf = make([]int16, ltpMemLength+laShape+frameLength)
		silkVADInit(&st.vad)
		st.nsq = NSQState{}
		st.frameCounter = 0
		st.prevSignalType = typeNoVoiceActivity
		st.prevLag = 0
		st.noSpeechCounter = 0
		st.inDTX = 0
		st.firstFrameAfterReset = true
		st.ltpCorrQ15 = 0
		st.sumLogGainQ7 = 0
		st.prevNLSFqQ15 = [maxLPCOrder]int16{}
		st.lastGainIndex = 10
		st.harmShapeGainSmthQ16 = 0
		st.tiltSmthQ16 = 0
		st.ecPrevLagIndex = 0
		st.ecPrevSignalType = typeNoVoiceActivity
		st.lbrrPrevLastGainIndex = 10
		st.speechActivityQ8 = 0
		st.inputTiltQ15 = 0
		st.inputQualityBandsQ15 = [vadNBands]int32{}
		// control_codec first-frame reset (control_codec.c:254,257).
		st.nsq.prevGainQ16 = 1 << 16
		st.nsq.lagPrev = 100
		st.initialized = true
	}
	return st
}

// resetFixedState forces the integer SILK state to re-initialize on the next
// frame. Called from Encoder.Reset under the tag.
func (e *Encoder) resetFixedState() {
	if e.fixed != nil {
		e.fixed.initialized = false
	}
}

// buildFixedInputBuf converts the float frame to int16 (RES2INT16 / FLOAT2INT16)
// and applies silk_LP_variable_cutoff, returning the int16 frame that libopus
// places at inputBuf+1 just before insertion into x_buf.
func (e *Encoder) buildFixedInputBuf(pcm []float32, frameSamples int) []int16 {
	buf := ensureInt16Slice(&e.scratchLPInt16, frameSamples)
	if e.fixedStereoInt16In != nil {
		// Integer stereo front-end (silkStereoLRToMS) already produced the exact
		// int16 mid/side samples libopus writes into inputBuf+2; consume them
		// verbatim so no float round-trip can perturb the LSBs.
		src := e.fixedStereoInt16In
		for i := 0; i < frameSamples; i++ {
			if i < len(src) {
				buf[i] = src[i]
			} else {
				buf[i] = 0
			}
		}
		e.fixedStereoInt16In = nil
	} else {
		for i := 0; i < frameSamples; i++ {
			if i < len(pcm) {
				buf[i] = float32ToInt16(pcm[i])
			} else {
				buf[i] = 0
			}
		}
	}
	// silk_LP_variable_cutoff operates in place on inputBuf+1.
	e.lpState.LPVariableCutoff(buf, frameSamples)
	return buf
}

// encodeFrameFixedBody runs the FIXED_POINT analysis + rate-control body for one
// SILK frame and finalizes via the shared tail. It is the integer-path
// counterpart of the float analysis block in EncodeFrame.
func (e *Encoder) encodeFrameFixedBody(
	pcm []float32,
	frameSamples, numSubframes, subframeSamples, payloadSizeMs int,
	condCoding int,
	vadFlag, firstFrameAfterReset, useSharedEncoder, blockUseCBR bool,
) []byte {
	st := e.ensureFixedState()
	fsKHz := st.fsKHz
	ltpMemLength := ltpMemLengthMs * fsKHz
	laShape := laShapeMs * fsKHz
	laPitch := laPitchMs * fsKHz
	pitchLPCWinLength := (ltpMemLengthMs + (laPitchMs << 1)) * fsKHz

	// reducedDependency / first packet: code the first frame as
	// first_frame_after_reset (libopus enc_API.c:268).
	if firstFrameAfterReset {
		st.firstFrameAfterReset = true
	}

	// Build the int16 input frame (RES2INT16 + LP variable cutoff), then insert
	// into x_buf at x_frame + LA_SHAPE_MS*fs_kHz (encode_frame_FIX.c).
	inputBuf := e.buildFixedInputBuf(pcm, frameSamples)
	xFrame := ltpMemLength
	insert := st.xBuf[xFrame+laShape : xFrame+laShape+frameSamples]
	copy(insert, inputBuf)

	predictLPCOrder := 10
	if int(e.lpcOrder) == 16 {
		predictLPCOrder = 16
	}
	pitchEstLPCOrder := int(e.pitchEstimationLPCOrder)
	if pitchEstLPCOrder > predictLPCOrder {
		pitchEstLPCOrder = predictLPCOrder
	}

	opusVADActivity := 1
	if !vadFlag {
		opusVADActivity = vadNoActivity
	}

	useCBRInt := 0
	if blockUseCBR {
		useCBRInt = 1
	}

	lbrrFlagInt := int32(0)
	if e.lbrrEnabled {
		lbrrFlagInt = 1
	}

	ps := &silkEncodeFramePayloadFIXState{
		silkEncodeFrameFIXState: silkEncodeFrameFIXState{
			fsKHz:                       fsKHz,
			frameLength:                 frameSamples,
			subfrLength:                 subframeSamples,
			nbSubfr:                     numSubframes,
			ltpMemLength:                ltpMemLength,
			laPitch:                     laPitch,
			laShape:                     laShape,
			pitchLPCWinLength:           pitchLPCWinLength,
			pitchEstimationLPCOrder:     pitchEstLPCOrder,
			predictLPCOrder:             predictLPCOrder,
			shapingLPCOrder:             int(e.shapingLPCOrder),
			shapeWinLength:              int(e.shapeWinLength),
			complexity:                  int(e.pitchEstimationComplexity),
			nStatesDelayedDecision:      int(e.nStatesDelayedDecision),
			warpingQ16:                  e.warpingQ16,
			useCBR:                      useCBRInt,
			nlsfMSVQSurvivors:           int(e.nlsfSurvivors),
			pitchEstimationThresholdQ16: e.pitchEstimationThresholdQ16,
			snrDBQ7:                     e.snrDBQ7,
			inputTiltQ15:                st.inputTiltQ15,
			packetLossPerc:              e.packetLossPercent,
			nFramesPerPacket:            e.nFramesPerPacket,
			lbrrFlag:                    lbrrFlagInt,
			condCoding:                  int32(condCoding),
			opusVADActivity:             opusVADActivity,
			frameCounter:                st.frameCounter,
			prevSignalType:              st.prevSignalType,
			prevLag:                     st.prevLag,
			speechActivityQ8:            st.speechActivityQ8,
			inputQualityBandsQ15:        st.inputQualityBandsQ15,
			noSpeechCounter:             st.noSpeechCounter,
			inDTX:                       st.inDTX,
			firstFrameAfterReset:        st.firstFrameAfterReset,
			ltpCorrQ15:                  st.ltpCorrQ15,
			sumLogGainQ7:                st.sumLogGainQ7,
			prevNLSFqQ15:                st.prevNLSFqQ15,
			harmShapeGainSmthQ16:        st.harmShapeGainSmthQ16,
			tiltSmthQ16:                 st.tiltSmthQ16,
			lastGainIndex:               st.lastGainIndex,
			vad:                         st.vad,
			nsq:                         st.nsq,
			vadInput:                    inputBuf,
			xBuf:                        st.xBuf,
		},
		ecPrevLagIndex:        st.ecPrevLagIndex,
		ecPrevSignalType:      st.ecPrevSignalType,
		lbrrEnabled:           e.lbrrEnabled,
		lbrrGainIncreases:     e.lbrrGainIncreases,
		lbrrPrevLastGainIndex: &st.lbrrPrevLastGainIndex,
		nFramesEncoded:        int(e.nFramesEncoded),
		lbrrPrevFrameHadLBRR:  e.nFramesEncoded > 0 && e.lbrrFlags[e.nFramesEncoded-1] != 0,
		rangeEncoder:          e.rangeEncoder,
		maxBits:               int(e.maxBits),
		useCBR:                blockUseCBR,
		bandwidth:             e.bandwidth,
	}

	// Snapshot the inputs at the moment the validated payload driver runs so a
	// parity test can replay them against the libopus oracle. Disabled in
	// production (gated by a test-only flag) to keep the hot path lean.
	if st.captureSnapshot {
		e.captureFixedSnapshot(st, ps, inputBuf, numSubframes, frameSamples, condCoding, predictLPCOrder, pitchEstLPCOrder, useCBRInt)
	}

	res := e.silkEncodeFramePayloadFIX(ps)

	// The SILK VAD header bit (and, for the stereo side channel, the mid-only
	// gate) must reflect the integer VAD decision computed inside the encode
	// body, not the Opus-level activity flag passed in. Persist it and patch the
	// standalone header from it below.
	st.lastFixedVADFlag = res.vadFlag != 0

	// Persist the integer cross-frame state back.
	fs := &ps.silkEncodeFrameFIXState
	st.vad = fs.vad
	st.nsq = fs.nsq
	st.frameCounter = fs.frameCounter
	st.prevSignalType = fs.prevSignalType
	st.prevLag = fs.prevLag
	st.noSpeechCounter = fs.noSpeechCounter
	st.inDTX = fs.inDTX
	st.firstFrameAfterReset = fs.firstFrameAfterReset
	st.ltpCorrQ15 = fs.ltpCorrQ15
	st.sumLogGainQ7 = fs.sumLogGainQ7
	st.prevNLSFqQ15 = fs.prevNLSFqQ15
	st.lastGainIndex = fs.lastGainIndex
	st.harmShapeGainSmthQ16 = fs.harmShapeGainSmthQ16
	st.tiltSmthQ16 = fs.tiltSmthQ16
	st.ecPrevLagIndex = ps.ecPrevLagIndex
	st.ecPrevSignalType = ps.ecPrevSignalType
	st.speechActivityQ8 = fs.speechActivityQ8

	// Mirror the float encoder's cross-frame fields read by the orchestration
	// (LBRR header, hybrid silk_info, bandwidth switch gate).
	e.previousGainIndex = fs.lastGainIndex
	e.previousLogGain = int32(fs.lastGainIndex)
	e.ecPrevSignalType = ps.ecPrevSignalType
	e.lastQuantOffsetType = int(fs.indicesQuantOffset)
	e.lastSeed = int8(fs.frameCounter-1) & 3
	e.isPreviousFrameVoiced = fs.indicesSignalType == int8(typeVoiced)
	e.lastSpeechActivityQ8 = fs.speechActivityQ8

	// Capture LBRR side info for this frame so the shared LBRR header machinery
	// (encodeLBRRData) can emit it in the NEXT packet.
	frameIdx := int(e.nFramesEncoded)
	if frameIdx >= 0 && frameIdx < maxFramesPerPacket {
		if res.lbrrFlag != 0 {
			e.lbrrFlags[frameIdx] = 1
			e.lbrrIndices[frameIdx] = res.lbrrIndices
			e.lbrrFrameLength[frameIdx] = int32(frameSamples)
			e.lbrrNbSubfr[frameIdx] = int32(numSubframes)
			dst := e.lbrrPulses[frameIdx]
			if cap(dst) < frameSamples {
				dst = make([]int8, frameSamples)
				e.lbrrPulses[frameIdx] = dst
			}
			dst = dst[:cap(dst)]
			copy(dst, res.lbrrPulses)
			for i := len(res.lbrrPulses); i < len(dst); i++ {
				dst[i] = 0
			}
		} else {
			e.lbrrFlags[frameIdx] = 0
		}
	}

	// Shift the integer x_buf left by frame_length, keeping ltp_mem + la_shape.
	keep := ltpMemLength + laShape
	copy(st.xBuf[:keep], st.xBuf[frameSamples:frameSamples+keep])

	return e.finalizeEncodeFrame(frameSamples, payloadSizeMs, res.vadFlag != 0, useSharedEncoder)
}

// FixedLastVADFlag reports the integer VAD decision of the most recent fixed
// encode-body call (1 == active), for the stereo orchestration's VAD header
// patch. It is meaningful only under the gopus_fixed_point build.
func (e *Encoder) FixedLastVADFlag() bool {
	if e.fixed == nil {
		return false
	}
	return e.fixed.lastFixedVADFlag
}

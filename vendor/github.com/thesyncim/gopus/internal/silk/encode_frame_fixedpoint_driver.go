//go:build gopus_fixed_point

package silk

// This file wires the bit-exact FIXED_POINT SILK per-frame driver
// (silkEncodeFramePayloadFIX) into the silk.Encoder channel state so that
// Encoder.encodeFrame codes byte-exact SILK frames matching the libopus
// FIXED_POINT encoder (silk/fixed/encode_frame_FIX.c). The packet-level flow of
// silk_Encode (PacketEncoder.Encode in enc_api.go: LBRR header and data,
// multi-frame loop, stereo front end, VAD/FEC header patch) is shared with the
// float path; only the per-frame analysis + rate-control body is replaced here.
//
// The driver maintains its own integer cross-frame state (int16 x_buf, VAD
// state, noise-shape smoothers, previous-NLSF history, NSQ state, ...) mirroring
// silk_encoder_state_FIX, fed from the int16-quantized input frame. This avoids
// any dependency on the float analysis history.

// silkEncoderFixedFields carries the FIXED_POINT integer SILK encode state held
// on the public Encoder under the gopus_fixed_point build.
type silkEncoderFixedFields struct {
	fixed *silkFixedEncodeState
}

// silkFixedEncodeState holds the persistent silk_encoder_state_FIX-equivalent
// state for the integer SILK encode path.
type silkFixedEncodeState struct {
	// scratch holds the reusable per-frame working buffers for the integer
	// encode path, grown once and reused across every frame of a packet.
	scratch *silkFixedEncodeScratch

	// fsKHz is the channel's internal rate (sCmn.fs_kHz).
	fsKHz int

	// Integer x_buf history (ltp_mem_length + la_shape + frame_length samples
	// at the highest internal rate), matching silk_encoder_state_FIX.x_buf. The
	// new frame is inserted at x_buf[ltp_mem_length + la_shape].
	xBuf []int16

	// Persistent NSQ state (silk_nsq_state).
	nsq NSQState

	// Mutable common-state carried across frames.
	frameCounter         int32
	prevSignalType       int32
	prevLag              int32
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
	numSubframes, frameSamples, condCoding, predictLPCOrder, pitchEstLPCOrder, useCBRInt, maxBits int,
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
	st.testMaxBits = maxBits
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
		MaxBits:              maxBits,
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

// FixedXBufForTest returns the integer x_buf history of the first channel. For
// parity tests only.
func (s *PacketEncoder) FixedXBufForTest() []int16 {
	return s.state[0].FixedXBufForTest()
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

// ensureFixedState returns the integer SILK state at the channel's current
// internal rate. Encoder.reset sets it up (resetFixedState); silk_setup_fs
// resets its analysis history on a rate change (resetFixedAnalysisHistory) and
// silk_setup_resamplers carries its x_buf over (xBufToInt16/xBufFromInt16).
func (e *Encoder) ensureFixedState() *silkFixedEncodeState {
	st := e.fixed
	st.fsKHz = int(e.fsKHz)
	return st
}

// prefillFrameFixed is the prefill branch of silk_encode_frame_FIX
// (silk/fixed/encode_frame_FIX.c): the frame counter advances, the LP-filtered
// frame enters x_buf and x_buf shifts, without analysis or entropy coding.
func (e *Encoder) prefillFrameFixed(in []int16) {
	st := e.ensureFixedState()
	st.frameCounter++
	keep := (ltpMemLengthMs + laShapeMs) * st.fsKHz
	frameSamples := len(in)
	copy(st.xBuf[keep:keep+frameSamples], in)
	copy(st.xBuf[:keep], st.xBuf[frameSamples:frameSamples+keep])
}

// resetFixedState returns the integer SILK state to the state
// silk_init_encoder leaves it, with x_buf sized for the highest internal rate.
// Called from Encoder.reset under the tag.
func (e *Encoder) resetFixedState() {
	st := e.fixed
	if st == nil {
		st = &silkFixedEncodeState{}
		e.fixed = st
	}
	st.fsKHz = 0
	clear(ensureInt16Slice(&st.xBuf, (ltpMemLengthMs+laShapeMs)*maxFsKHz+maxFrameLength))
	st.nsq = NSQState{}
	st.frameCounter = 0
	st.prevSignalType = typeNoVoiceActivity
	st.prevLag = 0
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
	st.nsq.prevGainQ16 = 1 << 16
	st.nsq.lagPrev = 100
}

// xBufToInt16 copies the first len(dst) samples of the integer x_buf for
// silk_setup_resamplers (silk/control_codec.c), which resamples the FIXED_POINT
// x_buf in place.
func (e *Encoder) xBufToInt16(dst []int16) {
	copy(dst, e.fixed.xBuf)
}

// xBufFromInt16 stores the resampled x_buf of silk_setup_resamplers
// (silk/control_codec.c) back into the integer x_buf.
func (e *Encoder) xBufFromInt16(src []int16) {
	copy(e.fixed.xBuf, src)
}

// encodeFrameFixedBody runs the FIXED_POINT analysis + rate-control body for one
// SILK frame, the integer-path counterpart of the float analysis block in
// encodeFrame. in is the LP-filtered frame (inputBuf+1); the VAD decision of the
// packet-level silk_encode_do_VAD step is already in the channel state.
func (e *Encoder) encodeFrameFixedBody(
	in []int16,
	numSubframes, subframeSamples, condCoding int,
	vadFlag, firstFrameAfterReset bool,
	maxBits int,
	useCBR bool,
) int32 {
	st := e.ensureFixedState()
	fsKHz := st.fsKHz
	frameSamples := len(in)
	ltpMemLength := ltpMemLengthMs * fsKHz
	laShape := laShapeMs * fsKHz
	laPitch := laPitchMs * fsKHz
	// silk/control_codec.c uses FIND_PITCH_LPC_WIN_MS for four-subframe
	// frames and FIND_PITCH_LPC_WIN_MS_2_SF for the two-subframe 10 ms path.
	pitchLPCWinLength := (ltpMemLengthMs + (laPitchMs << 1)) * fsKHz
	if numSubframes != maxNbSubfr {
		pitchLPCWinLength = (10 + (laPitchMs << 1)) * fsKHz
	}

	// reducedDependency / first packet: code the first frame as
	// first_frame_after_reset (libopus enc_API.c:268).
	if firstFrameAfterReset {
		st.firstFrameAfterReset = true
	}

	// Insert the frame into x_buf at x_frame + LA_SHAPE_MS*fs_kHz
	// (encode_frame_FIX.c).
	inputBuf := in
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

	signalType := int8(typeNoVoiceActivity)
	if vadFlag {
		signalType = int8(typeUnvoiced)
	}

	useCBRInt := 0
	if useCBR {
		useCBRInt = 1
	}
	// The noise shaping analysis reads sCmn.useCBR, the packet's CBR control
	// (silk_control_encoder); the rate control loop reads the block's useCBR.
	cmnUseCBR := 0
	if e.useCBR {
		cmnUseCBR = 1
	}
	// silk/control_codec.c:silk_setup_complexity enables NLSF interpolation
	// at complexity 4 and above. Analysis uses its configured la_shape;
	// x_buf insertion and history retain the fixed LA_SHAPE_MS lookahead.
	var useInterpolatedNLSFs int32
	if e.complexity >= 4 {
		useInterpolatedNLSFs = 1
	}

	ps := &silkEncodeFramePayloadFIXState{
		silkEncodeFrameFIXState: silkEncodeFrameFIXState{
			fsKHz:                       fsKHz,
			frameLength:                 frameSamples,
			subfrLength:                 subframeSamples,
			nbSubfr:                     numSubframes,
			ltpMemLength:                ltpMemLength,
			laPitch:                     laPitch,
			laShape:                     int(e.laShape),
			pitchLPCWinLength:           pitchLPCWinLength,
			pitchEstimationLPCOrder:     pitchEstLPCOrder,
			predictLPCOrder:             predictLPCOrder,
			shapingLPCOrder:             int(e.shapingLPCOrder),
			shapeWinLength:              int(e.shapeWinLength),
			complexity:                  int(e.pitchEstimationComplexity),
			nStatesDelayedDecision:      int(e.nStatesDelayedDecision),
			warpingQ16:                  e.warpingQ16,
			useCBR:                      cmnUseCBR,
			nlsfMSVQSurvivors:           int(e.nlsfSurvivors),
			useInterpolatedNLSFs:        useInterpolatedNLSFs,
			pitchEstimationThresholdQ16: e.pitchEstimationThresholdQ16,
			snrDBQ7:                     e.snrDBQ7,
			inputTiltQ15:                e.inputTiltQ15,
			packetLossPerc:              e.packetLossPercent,
			nFramesPerPacket:            e.nFramesPerPacket,
			lbrrFlag:                    int32(e.lbrrFlag),
			condCoding:                  int32(condCoding),
			vadDone:                     true,
			frameCounter:                st.frameCounter,
			prevSignalType:              st.prevSignalType,
			prevLag:                     st.prevLag,
			speechActivityQ8:            e.speechActivityQ8,
			inputQualityBandsQ15:        e.inputQualityBandsQ15,
			indicesSignalType:           signalType,
			firstFrameAfterReset:        st.firstFrameAfterReset,
			ltpCorrQ15:                  st.ltpCorrQ15,
			sumLogGainQ7:                st.sumLogGainQ7,
			prevNLSFqQ15:                st.prevNLSFqQ15,
			harmShapeGainSmthQ16:        st.harmShapeGainSmthQ16,
			tiltSmthQ16:                 st.tiltSmthQ16,
			lastGainIndex:               st.lastGainIndex,
			nsq:                         st.nsq,
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
		maxBits:               maxBits,
		useCBR:                useCBR,
		bandwidth:             e.bandwidth,
	}

	// Snapshot the inputs at the moment the validated payload driver runs so a
	// parity test can replay them against the libopus oracle. Disabled in
	// production (gated by a test-only flag) to keep the hot path lean.
	if st.captureSnapshot {
		e.captureFixedSnapshot(st, ps, inputBuf, numSubframes, frameSamples, condCoding, predictLPCOrder, pitchEstLPCOrder, useCBRInt, maxBits)
	}

	res := e.silkEncodeFramePayloadFIX(ps)

	// Persist the integer cross-frame state back.
	fs := &ps.silkEncodeFrameFIXState
	st.nsq = fs.nsq
	st.frameCounter = fs.frameCounter
	st.prevSignalType = fs.prevSignalType
	st.prevLag = fs.prevLag
	st.firstFrameAfterReset = fs.firstFrameAfterReset
	st.ltpCorrQ15 = fs.ltpCorrQ15
	st.sumLogGainQ7 = fs.sumLogGainQ7
	st.prevNLSFqQ15 = fs.prevNLSFqQ15
	st.lastGainIndex = fs.lastGainIndex
	st.harmShapeGainSmthQ16 = fs.harmShapeGainSmthQ16
	st.tiltSmthQ16 = fs.tiltSmthQ16
	st.ecPrevLagIndex = ps.ecPrevLagIndex
	st.ecPrevSignalType = ps.ecPrevSignalType

	// Mirror the float encoder's cross-frame fields read by the packet encoder
	// (silk_info outputs, silk_HP_variable_cutoff).
	e.previousGainIndex = fs.lastGainIndex
	e.ecPrevSignalType = ps.ecPrevSignalType
	e.lastQuantOffsetType = int(fs.indicesQuantOffset)
	e.lastSeed = res.seed
	e.isPreviousFrameVoiced = fs.indicesSignalType == int8(typeVoiced)
	e.pitchState.prevLag = fs.prevLag

	// Capture LBRR side info for this frame so the packet encoder can emit it
	// with the NEXT packet.
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

	return e.finishFrame(frameSamples)
}

package silk

import "github.com/thesyncim/gopus/internal/rangecoding"

// VADFrameState carries per-frame SILK VAD-derived controls.
// It mirrors the state produced by silk_encode_do_VAD_Fxx in libopus.
type VADFrameState struct {
	SpeechActivityQ8     int32
	InputTiltQ15         int32
	InputQualityBandsQ15 [4]int32
	Valid                bool
}

// VADFrameAnalyzer computes libopus-style per-frame VAD state for a SILK block.
// It is used by the Opus-level stereo packet wrapper to run VAD after
// mid/side conversion on the actual coded signals.
type VADFrameAnalyzer func(frame []float32, frameSamples, fsKHz int) (VADFrameState, bool)

// Encoder encodes PCM audio to SILK frames.
// It maintains state across frames that mirrors the decoder for proper
// synchronized prediction of gains, LSF, and stereo weights.
//
// Reference: RFC 6716 Section 5.2, draft-vos-silk-01
type Encoder struct {
	// silkEncoderFixedFields carries the FIXED_POINT integer SILK encode state
	// added under the gopus_fixed_point build. It is empty (zero-size) in the
	// default build, keeping the Encoder struct byte-unchanged.
	silkEncoderFixedFields

	// Range encoder reference (set per frame)
	rangeEncoder *rangecoding.Encoder

	// lastRng holds the final range coder state after encoding.
	// This is captured before calling Done() which clears the state.
	lastRng uint32

	// Frame state (persists across frames, mirrors decoder)
	haveEncoded           bool  // True after first frame encoded
	previousLogGain       int32 // Last subframe gain (for delta coding) - legacy
	previousGainIndex     int8  // Previous gain quantization index [0, 63] (libopus sShape.LastGainIndex)
	isPreviousFrameVoiced bool  // Was previous frame voiced
	variableHPSmth1Q15    int32 // Smoothed log-domain HP cutoff estimate (silk_HP_variable_cutoff)
	ecPrevLagIndex        int16 // Previous lag index for conditional pitch coding
	ecPrevSignalType      int32 // Previous signal type for conditional pitch coding
	lastQuantOffsetType   int   // Last frame's quantization offset type (for hybrid silk_info)
	lastSeed              int8  // Last frame's encoded random seed
	frameCounter          int32 // Frame counter for seed generation (seed = frameCounter & 3)

	// LPC state
	lpcOrder   int32   // Current LPC order (10 for NB/MB, 16 for WB)
	prevLSFQ15 []int16 // Previous frame LSF (Q15) for interpolation

	// Stereo state
	prevStereoWeights [2]int16       // Previous w0, w1 stereo weights (Q13)
	stereo            stereoEncState // Full stereo encoder state for LP filtering

	// LP variable cutoff filter state (for smooth bandwidth transitions)
	lpState LPState

	// Pitch analysis state
	pitchState       PitchAnalysisState // State for pitch estimation across frames
	pitchAnalysisBuf []float32          // History buffer for pitch analysis (LTP memory + frame)

	// VAD-derived state (optional, provided by Opus-level encoder)
	speechActivityQ8     int32    // Speech activity in Q8 (0-255)
	inputTiltQ15         int32    // Spectral tilt in Q15 from VAD
	inputQualityBandsQ15 [4]int32 // Quality in each VAD band (Q15)
	speechActivitySet    bool     // Whether VAD-derived state was explicitly set
	lastSpeechActivityQ8 int32    // Most recent encoded frame activity, for packet-level switch gating

	// NSQ (Noise Shaping Quantization) state
	nsqState        *NSQState        // Noise shaping quantizer state for proper libopus-matching
	noiseShapeState *NoiseShapeState // Noise shaping analysis state for adaptive parameters

	// Encoder control parameters (persists across frames)
	snrDBQ7                  int32 // Target SNR in dB (Q7 format, e.g., 25 dB = 25 * 128)
	targetRateBps            int32 // Target bitrate (per channel) for SNR control
	lastControlTargetRateBps int32 // Last per-frame target rate used for SNR control
	preAdjustedTargetRateBps int32 // One-shot externally adjusted target for shared stereo packet control
	useCBR                   bool  // Constant Bitrate mode
	blockUseCBR              bool  // Per-block CBR flag for 40/60 ms packet budgeting
	// reducedDependency mirrors libopus encControl->reducedDependency.
	// When enabled, the first frame of each packet is coded as
	// first_frame_after_reset without resetting the full encoder state.
	reducedDependency bool
	// forceFirstFrameAfterReset is latched at packet start and consumed by the
	// first encoded frame in that packet.
	forceFirstFrameAfterReset bool
	ltpCorr                   float32 // LTP correlation from pitch analysis [0, 1]
	sumLogGainQ7              int32   // Sum log gain for LTP quantization
	complexity                int32   // Encoder complexity (0-10)
	nStatesDelayedDecision    int32   // Delayed decision states (libopus control_codec)

	// Pitch estimation tuning (mirrors libopus control_codec.c)
	pitchEstimationComplexity   int32
	pitchEstimationThresholdQ16 int32
	pitchEstimationLPCOrder     int32

	// Noise shaping analysis tuning (mirrors libopus control_codec.c)
	shapingLPCOrder int32
	laShape         int32
	shapeWinLength  int32
	warpingQ16      int32
	nlsfSurvivors   int32

	// LPC analysis results (for gain computation from prediction residual)
	lastTotalEnergy float32 // C0 from Burg analysis, narrowed to silk_float storage
	lastInvGain     float32 // Inverse prediction gain, narrowed to silk_float storage
	lastLPCGain     float32 // Initial prediction gain from pitch analysis (silk_float)
	lastNumSamples  int32   // Number of samples analyzed

	// Analysis buffers (encoder-specific)
	inputBuffer     []float32 // Noise shaping lookahead buffer (x_buf in libopus)
	lpcState        []float32 // LPC filter state for residual computation
	scratchPCMQuant []float32 // Quantized PCM (int16 round-trip) for SILK entry

	// Bandwidth configuration
	bandwidth  Bandwidth
	sampleRate int32

	// FEC/LBRR (Low Bitrate Redundancy) state
	// LBRR provides forward error correction by encoding redundant data
	// for the previous frame at a lower quality in the current packet.
	// Reference: libopus silk/structs.h silk_encoder_state
	useFEC                bool                                // Enable in-band FEC (LBRR)
	lbrrEnabled           bool                                // LBRR currently active (depends on bitrate/loss)
	lbrrPrevPacketHadLBRR bool                                // Previous packet had LBRR enabled (silk_setup_LBRR LBRR_in_previous_packet)
	lbrrLTPRoundLoss      bool                                // Use LBRR round-loss in LTP scale (previous packet had LBRR)
	lbrrGainIncreases     int32                               // Gain increase for LBRR encoding
	lbrrPrevLastGainIdx   int8                                // Previous frame's last gain index for LBRR
	lbrrFlags             [maxFramesPerPacket]int32           // LBRR flags per frame in packet
	lbrrFlag              int8                                // LBRR flag for current packet header
	lbrrIndices           [maxFramesPerPacket]sideInfoIndices // LBRR indices per frame
	lbrrPulses            [maxFramesPerPacket][]int8          // LBRR pulses per frame
	lbrrFrameLength       [maxFramesPerPacket]int32           // LBRR frame length per frame
	lbrrNbSubfr           [maxFramesPerPacket]int32           // LBRR subframe count per frame
	packetLossPercent     int32                               // Expected packet loss (0-100)
	nFramesEncoded        int32                               // Number of frames encoded in current packet
	nFramesPerPacket      int32                               // Number of frames per packet
	// Stereo packet condCoding uses mid nFramesEncoded at block start (libopus enc_API.c).
	stereoCondMid              *Encoder
	stereoCondMidFramesEncoded int32
	stereoChannelIdx           int32
	stereoPrevDecodeOnlyMiddle int32

	// Scratch buffers for zero-allocation encoding
	scratchPaddedPulses []int8  // encodePulses: padded pulses
	scratchAbsPulses    []int32 // encodePulses: absolute value pulses (opus_int-width)
	scratchSumPulses    []int32 // encodePulses: sum per shell block (opus_int-width)
	scratchNRshifts     []int32 // encodePulses: right shifts per shell block (opus_int-width)
	scratchLSFQ15       []int16 // lpcToLSF: LSF result in Q15
	scratchLPCQ16       []int32 // silkA2NLSF: LPC coefficients in Q16

	// Pitch detection scratch buffers
	scratchFrame8kHz      []float32 // detectPitch: downsampled to 8kHz
	scratchFrame4kHz      []float32 // detectPitch: downsampled to 4kHz
	scratchFrame16Fix     []int16   // detectPitch: input in int16 scale
	scratchFrame8Fix      []int16   // detectPitch: 8kHz int16 samples
	scratchFrame4Fix      []int16   // detectPitch: 4kHz int16 samples
	scratchResampler      []int32   // detectPitch: resampler buffer (int32)
	scratchPitchC         []float32 // detectPitch: autocorrelation (silk_float)
	scratchDSrch          []int32   // detectPitch: candidate lags
	scratchDComp          []int16   // detectPitch: expanded search
	scratchPitchLags      []int32   // detectPitch: output pitch lags
	scratchPitchCorrSt3   []float32 // detectPitch: stage3 correlations (silk_float)
	scratchPitchEnergySt3 []float32 // detectPitch: stage3 energies (silk_float)
	scratchPitchXcorr     []float32 // detectPitch: celt_pitch_xcorr scratch

	// Shell encoder scratch buffers (fixed sizes)
	scratchShellPulses1 [8]int32 // shellEncoder: level 1 (opus_int-width)
	scratchShellPulses2 [4]int32 // shellEncoder: level 2 (opus_int-width)
	scratchShellPulses3 [2]int32 // shellEncoder: level 3 (opus_int-width)
	scratchShellPulses4 [1]int32 // shellEncoder: level 4 (opus_int-width)

	// NSQ (computeNSQExcitation) scratch buffers
	scratchInputQ0          []int16 // PCM converted to int16
	scratchGainsUnqQ16      []int32 // unquantized gains in Q16 format
	scratchGainsQ16         []int32 // gains in Q16 format
	scratchLBRRGainsQ16     []int32 // LBRR dequantized gains (must not alias scratchGainsQ16)
	scratchPitchL           []int32 // pitch lags for NSQ
	scratchArShpQ13         []int16 // AR shaping coefficients
	scratchLtpCoefQ14       []int16 // LTP coefficients
	scratchPredCoefQ12      []int16 // prediction coefficients
	scratchHarmShapeGainQ14 []int32 // harmonic shaping gain (opus_int-width)
	scratchTiltQ14          []int32 // tilt values (opus_int-width)
	scratchLfShpQ14         []int32 // low-frequency shaping
	scratchEcBufCopy        []byte  // range encoder buffer snapshot

	// LPC/Burg scratch buffers. The Burg work arrays mirror C double arrays
	// in libopus silk/float/burg_modified_FLP.c; input/output stay silk_float.
	scratchLpcQ12        []int16     // computeLPCAndNLSFWithInterp: output LPC Q12
	scratchBurgAf        []silkCReal // burgModifiedFLPZeroAllocF32: Af buffer
	scratchBurgCFirstRow []silkCReal // burgModifiedFLPZeroAllocF32: CFirstRow
	scratchBurgCLastRow  []silkCReal // burgModifiedFLPZeroAllocF32: CLastRow
	scratchBurgCAf       []silkCReal // burgModifiedFLPZeroAllocF32: CAf
	scratchBurgCAb       []silkCReal // burgModifiedFLPZeroAllocF32: CAb
	scratchBurgResult    []float32   // burgModifiedFLPZeroAllocF32: result (silk_float)

	// LTP analysis scratch buffers
	scratchPitchRes32   []float32 // Pitch analysis: residual as float32
	scratchPitchInput32 []float32 // Pitch analysis: input buffer (float32)
	scratchPitchWsig32  []float32 // Pitch analysis: windowed signal (float32)
	scratchPitchAuto32  []float32 // Pitch analysis: autocorrelation (float32)
	scratchPitchRefl32  []float32 // Pitch analysis: reflection coefficients (float32)
	scratchPitchA32     []float32 // Pitch analysis: LPC coefficients (float32)

	scratchLtpResF32    []float32 // LTP analysis: LTP residual with pre-length
	scratchLpcResF32    []float32 // Residual energy: LPC residual scratch (float32)
	scratchResNrg       []float32 // Gain processing: residual energies (silk_float)
	scratchPredCoefF32A []float32 // Gain processing: LPC coeffs (first half, float32)
	scratchPredCoefF32B []float32 // Gain processing: LPC coeffs (second half, float32)

	// A2NLSF scratch buffers (used by a2nlsfFLPInto / silkA2NLSFInto)
	scratchA2nlsfP    [9]int32  // silkA2NLSFInto: P polynomial (dd+1, max dd=8)
	scratchA2nlsfQ    [9]int32  // silkA2NLSFInto: Q polynomial
	scratchA2nlsfAQ16 [16]int32 // a2nlsfFLPInto: LPC Q16 conversion
	scratchA2nlsfNLSF [16]int16 // a2nlsfFLPInto: NLSF result

	// FindLPC interpolation scratch buffers
	scratchLpcX        []float32   // FindLPC input (silk_float)
	scratchNlsf0Q15    [16]int16   // interpolated NLSF
	scratchLpcATmp     [16]float32 // silk_NLSF2A_FLP output (silk_float)
	scratchLpcAQ12     [16]int16   // silk_NLSF2A_FLP fixed bridge coefficients
	scratchLpcResidual []float32   // LPC residual for energy (silk_float)

	// LSF quantization scratch buffers
	scratchLsfResiduals   []int32 // computeStage2ResidualsLibopus: residuals
	scratchEcIx           []int16 // computeStage2ResidualsLibopus / NLSF decode: ecIx
	scratchPredQ8         []uint8 // computeStage2ResidualsLibopus / NLSF decode: predQ8
	scratchResQ10         []int16 // computeStage2ResidualsLibopus / NLSF decode: resQ10
	scratchNLSFIndices    []int8  // NLSF decode indices (stage1 + residuals)
	scratchNLSFWeights    []int16 // NLSF VQ weights (Laroia)
	scratchNLSFWeightsTmp []int16 // NLSF weights for interpolated vector
	scratchNLSFTempQ15    []int16 // Interpolated NLSF scratch

	// Gain encoding scratch buffers
	scratchGains   []float32 // computeSubframeGains: output gains
	scratchGainInd []int8    // silkGainsQuant: gain indices

	// Rate control loop scratch buffers
	// Bit reservoir and rate control state (libopus parity)
	nBitsExceeded     int32 // Bits produced in excess of target
	nBitsUsedLBRR     int32 // Exponential moving average of LBRR overhead bits
	currNBitsUsedLBRR int32 // LBRR header bits emitted for the current frame (libopus enc_API.c curr_nBitsUsedLBRR; non-zero only on the frame that writes the packet's LBRR header)
	maxBits           int32 // Maximum bits allowed for current frame
	useVBR            bool

	// LP variable cutoff filter scratch buffer
	scratchLPInt16 []int16 // LP filter: int16 conversion for biquad filter

	// Opus-level bandwidth switch gate mirrored from silk/enc_API.c.
	timeSinceSwitchAllowedMS int32
	allowBandwidthSwitch     bool

	// Output buffer scratch (standalone SILK mode)
	scratchOutput       []byte              // EncodeFrame: range encoder output
	scratchRangeEncoder rangecoding.Encoder // EncodeFrame: reusable range encoder

	// Stereo scratch buffers for zero-allocation stereo encoding
	scratchStereoMid      []float32 // mid channel (frameLength+2)
	scratchStereoSide     []float32 // side channel (frameLength+2)
	scratchStereoMidOut   []float32 // mid output (frameLength)
	scratchStereoSideOut  []float32 // side output (frameLength)
	scratchStereoMidQ0    []int16   // mid channel in Q0 (frameLength+2)
	scratchStereoSideQ0   []int16   // side channel in Q0 (frameLength+2)
	scratchStereoLPMidQ0  []int16   // LP filtered mid in Q0
	scratchStereoHPMidQ0  []int16   // HP filtered mid in Q0
	scratchStereoLPSideQ0 []int16   // LP filtered side in Q0
	scratchStereoHPSideQ0 []int16   // HP filtered side in Q0
	scratchStereoLPMid    []float32 // LP filtered mid
	scratchStereoHPMid    []float32 // HP filtered mid
	scratchStereoLPSide   []float32 // LP filtered side
	scratchStereoHPSide   []float32 // HP filtered side
	scratchStereoPadLeft  []float32 // padding buffer for left
	scratchStereoPadRight []float32 // padding buffer for right
}

// ensureInt8Slice ensures the slice has at least n elements.
func ensureInt8Slice(buf *[]int8, n int) []int8 {
	if cap(*buf) < n {
		*buf = make([]int8, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureCRealSlice is reserved for SILK FLP helpers whose libopus
// source explicitly declares C double work arrays.
func ensureCRealSlice(buf *[]silkCReal, n int) []silkCReal {
	if cap(*buf) < n {
		*buf = make([]silkCReal, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureInt16Slice ensures the slice has at least n elements.
func ensureInt16Slice(buf *[]int16, n int) []int16 {
	if cap(*buf) < n {
		*buf = make([]int16, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureFloat32Slice ensures the slice has at least n elements.
func ensureFloat32Slice(buf *[]float32, n int) []float32 {
	if cap(*buf) < n {
		*buf = make([]float32, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureInt32Slice ensures the slice has at least n elements.
func ensureInt32Slice(buf *[]int32, n int) []int32 {
	if cap(*buf) < n {
		*buf = make([]int32, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureUint8Slice ensures the slice has at least n elements.
func ensureUint8Slice(buf *[]uint8, n int) []uint8 {
	if cap(*buf) < n {
		*buf = make([]uint8, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureByteSlice ensures the slice has at least n elements.
func ensureByteSlice(buf *[]byte, n int) []byte {
	if cap(*buf) < n {
		*buf = make([]byte, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// NewEncoder creates a new SILK encoder with proper initial state.
func NewEncoder(bandwidth Bandwidth) *Encoder {
	config := GetBandwidthConfig(bandwidth)
	// Frame samples = sampleRate * 20ms / 1000
	frameSamples := config.SampleRate * 20 / 1000

	// Pre-allocate LBRR pulse buffers
	lbrrPulses := [maxFramesPerPacket][]int8{}
	for i := range lbrrPulses {
		lbrrPulses[i] = make([]int8, maxFrameLength)
	}

	// Pitch analysis buffer: LTP memory + max frame (20ms).
	// Lookahead is zero-padded during residual computation.
	frameMs := 20
	fsKHz := config.SampleRate / 1000
	pitchBufSamples := (ltpMemLengthMs + frameMs) * fsKHz
	shapeBufSamples := (ltpMemLengthMs+laShapeMs)*fsKHz + frameSamples
	pitchResSamples := pitchBufSamples + laPitchMs*fsKHz
	enc := &Encoder{
		prevLSFQ15:        make([]int16, config.LPCOrder),
		inputBuffer:       make([]float32, shapeBufSamples), // Noise shaping buffer with lookahead
		lpcState:          make([]float32, config.LPCOrder),
		nsqState:          NewNSQState(),                    // Initialize NSQ state
		noiseShapeState:   NewNoiseShapeState(),             // Initialize noise shaping state
		pitchAnalysisBuf:  make([]float32, pitchBufSamples), // Pitch analysis buffer
		scratchPitchRes32: make([]float32, pitchResSamples),
		bandwidth:         bandwidth,
		sampleRate:        int32(config.SampleRate),
		lpcOrder:          int32(config.LPCOrder),
		// Keep reset parity with libopus.
		pitchState:               PitchAnalysisState{prevLag: 0},
		snrDBQ7:                  0, // Match libopus zero-initialization (silk_init_encoder memset 0)
		lastControlTargetRateBps: 0,
		nFramesPerPacket:         1, // Default: 1 frame per packet (20ms)
		lbrrPulses:               lbrrPulses,
		lbrrGainIncreases:        7, // Default gain increase for LBRR
		previousGainIndex:        0,
	}
	enc.variableHPSmth1Q15 = initVariableHPSmth1Q15()
	enc.SetComplexity(10)
	resetStereoEncState(&enc.stereo)
	// Match Opus-level init timing: before first control pass, libopus keeps
	// sNSQ zero-initialized. control_codec sets prev_gain_Q16 on first frame.
	if enc.nsqState != nil {
		enc.nsqState.prevGainQ16 = 0
	}
	// Set default bitrate matching libopus (opus_encoder.c: silk_mode.bitRate = 25000).
	// This ensures controlSNR is called on the first frame, matching libopus behavior
	// where silk_control_SNR is always invoked before encoding.
	enc.targetRateBps = 25000
	return enc
}

// Reset clears encoder state for a new stream.
func (e *Encoder) Reset() {
	e.resetFixedState()
	e.haveEncoded = false
	e.previousLogGain = 0
	e.previousGainIndex = 0
	e.isPreviousFrameVoiced = false
	e.variableHPSmth1Q15 = initVariableHPSmth1Q15()
	e.ecPrevLagIndex = 0
	e.ecPrevSignalType = 0
	e.targetRateBps = 0
	e.lastControlTargetRateBps = 0
	e.preAdjustedTargetRateBps = 0
	e.snrDBQ7 = 0
	e.sumLogGainQ7 = 0
	e.forceFirstFrameAfterReset = false

	for i := range e.prevLSFQ15 {
		e.prevLSFQ15[i] = 0
	}
	for i := range e.lpcState {
		e.lpcState[i] = 0
	}
	for i := range e.inputBuffer {
		e.inputBuffer[i] = 0
	}
	e.prevStereoWeights = [2]int16{0, 0}
	resetStereoEncState(&e.stereo)
	e.speechActivityQ8 = 0
	e.lastSpeechActivityQ8 = 0
	e.inputTiltQ15 = 0
	e.inputQualityBandsQ15 = [4]int32{}
	e.speechActivitySet = false
	e.timeSinceSwitchAllowedMS = 0
	e.allowBandwidthSwitch = false
	// Keep reset parity with libopus: prevLag resets to 0.
	e.pitchState = PitchAnalysisState{prevLag: 0}
	for i := range e.pitchAnalysisBuf {
		e.pitchAnalysisBuf[i] = 0
	}
	if e.nsqState != nil {
		e.nsqState.Reset() // Reset NSQ state
		// Match Opus-level reset timing: prev_gain_Q16 is applied during first
		// control pass after reset, not immediately on Reset().
		e.nsqState.prevGainQ16 = 0
	}
	if e.noiseShapeState != nil {
		e.noiseShapeState.Reset()
	}
	e.ltpCorr = 0

	// Reset FEC/LBRR state
	e.lbrrEnabled = false
	e.lbrrPrevPacketHadLBRR = false
	e.lbrrPrevLastGainIdx = 10 // Default gain index (same as decoder reset)
	e.lbrrFlag = 0
	e.nFramesEncoded = 0
	for i := range e.lbrrFlags {
		e.lbrrFlags[i] = 0
	}
	for i := range e.lbrrIndices {
		e.lbrrIndices[i] = sideInfoIndices{}
	}
	for i := range e.lbrrFrameLength {
		e.lbrrFrameLength[i] = 0
		e.lbrrNbSubfr[i] = 0
	}
	for i := range e.lbrrPulses {
		for j := range e.lbrrPulses[i] {
			e.lbrrPulses[i][j] = 0
		}
	}
}

// ResetTransitionPrefillState clears the low-level fields that libopus
// reinitializes during the CELT->SILK/HYBRID prefill reset path, while leaving
// packet-level controls owned by the Opus wrapper intact.
func (e *Encoder) ResetTransitionPrefillState() {
	e.lastQuantOffsetType = 0
	e.frameCounter = 0
	e.lpState = LPState{}
}

func resetStereoEncState(st *stereoEncState) {
	*st = stereoEncState{}
	st.midSideAmpQ0 = [4]int32{0, 1, 0, 1}
	st.smthWidthQ14 = 16384
}

// ResetStereoSideAfterMidOnly mirrors libopus enc_API.c when stereo side coding
// resumes after one or more mid-only frames. It preserves the long-lived
// encoder instance while clearing the side-channel state that libopus resets on
// re-entry.
func (e *Encoder) ResetStereoSideAfterMidOnly() {
	if e == nil {
		return
	}
	if e.noiseShapeState != nil {
		e.noiseShapeState.Reset()
	}
	if e.nsqState != nil {
		e.nsqState.Reset()
		e.nsqState.lagPrev = 100
		e.nsqState.prevGainQ16 = 1 << 16
	}
	for i := range e.prevLSFQ15 {
		e.prevLSFQ15[i] = 0
	}
	e.lpState.InLPState = [2]int32{}
	e.pitchState.prevLag = 100
	e.previousGainIndex = 10
	e.previousLogGain = 0
	e.ecPrevLagIndex = 0
	e.ecPrevSignalType = typeNoVoiceActivity
	e.isPreviousFrameVoiced = false
	e.forceFirstFrameAfterReset = true
	// Under the gopus_fixed_point build also reset the integer side-channel state
	// (sShape/sNSQ/prev_NLSFq/sLP, LastGainIndex, prevLag) exactly as libopus
	// enc_API.c does when side coding resumes. No-op on the float build.
	e.resetStereoSideFixedState()
}

// SetComplexity sets the SILK encoder complexity (0-10) and related pitch parameters.
func (e *Encoder) SetComplexity(complexity int) {
	if complexity < 0 {
		complexity = 0
	}
	if complexity > 10 {
		complexity = 10
	}
	e.complexity = int32(complexity)

	fsKHz := max(e.sampleRate/1000, 1)

	switch {
	case complexity < 1:
		e.pitchEstimationComplexity = 0
		e.pitchEstimationThresholdQ16 = 52429
		e.pitchEstimationLPCOrder = 6
		e.shapingLPCOrder = 12
		e.laShape = int32(3 * fsKHz)
		e.nStatesDelayedDecision = 1
		e.warpingQ16 = 0
		e.nlsfSurvivors = 2
	case complexity < 2:
		e.pitchEstimationComplexity = 1
		e.pitchEstimationThresholdQ16 = 49807
		e.pitchEstimationLPCOrder = 8
		e.shapingLPCOrder = 14
		e.laShape = int32(5 * fsKHz)
		e.nStatesDelayedDecision = 1
		e.warpingQ16 = 0
		e.nlsfSurvivors = 3
	case complexity < 3:
		e.pitchEstimationComplexity = 0
		e.pitchEstimationThresholdQ16 = 52429
		e.pitchEstimationLPCOrder = 6
		e.shapingLPCOrder = 12
		e.laShape = int32(3 * fsKHz)
		e.nStatesDelayedDecision = 2
		e.warpingQ16 = 0
		e.nlsfSurvivors = 2
	case complexity < 4:
		e.pitchEstimationComplexity = 1
		e.pitchEstimationThresholdQ16 = 49807
		e.pitchEstimationLPCOrder = 8
		e.shapingLPCOrder = 14
		e.laShape = int32(5 * fsKHz)
		e.nStatesDelayedDecision = 2
		e.warpingQ16 = 0
		e.nlsfSurvivors = 4
	case complexity < 6:
		e.pitchEstimationComplexity = 1
		e.pitchEstimationThresholdQ16 = 48497
		e.pitchEstimationLPCOrder = 10
		e.shapingLPCOrder = 16
		e.laShape = int32(5 * fsKHz)
		e.nStatesDelayedDecision = 2
		e.warpingQ16 = int32(float32(fsKHz) * float32(warpingMultiplier) * 65536.0)
		e.nlsfSurvivors = 6
	case complexity < 8:
		e.pitchEstimationComplexity = 1
		e.pitchEstimationThresholdQ16 = 47186
		e.pitchEstimationLPCOrder = 12
		e.shapingLPCOrder = 20
		e.laShape = int32(5 * fsKHz)
		e.nStatesDelayedDecision = 3
		e.warpingQ16 = int32(float32(fsKHz) * float32(warpingMultiplier) * 65536.0)
		e.nlsfSurvivors = 8
	default:
		e.pitchEstimationComplexity = 2
		e.pitchEstimationThresholdQ16 = 45875
		e.pitchEstimationLPCOrder = 16
		e.shapingLPCOrder = 24
		e.laShape = int32(5 * fsKHz)
		e.nStatesDelayedDecision = maxDelDecStates
		e.warpingQ16 = int32(float32(fsKHz) * float32(warpingMultiplier) * 65536.0)
		e.nlsfSurvivors = 16
	}

	if e.pitchEstimationLPCOrder > e.lpcOrder {
		e.pitchEstimationLPCOrder = e.lpcOrder
	}
	if e.shapingLPCOrder > maxShapeLpcOrder {
		e.shapingLPCOrder = maxShapeLpcOrder
	}
	if e.shapingLPCOrder < 2 {
		e.shapingLPCOrder = 2
	}
	if e.shapingLPCOrder&1 != 0 {
		e.shapingLPCOrder--
	}
	e.shapeWinLength = int32(subFrameLengthMs*fsKHz) + 2*e.laShape
}

// Complexity returns the current SILK complexity setting.
func (e *Encoder) Complexity() int {
	return int(e.complexity)
}

// SetRangeEncoder sets the range encoder for the current frame.
func (e *Encoder) SetRangeEncoder(re *rangecoding.Encoder) {
	e.rangeEncoder = re
}

// HaveEncoded returns whether at least one frame has been encoded.
func (e *Encoder) HaveEncoded() bool {
	return e.haveEncoded
}

func (e *Encoder) firstFrameAfterResetActive() bool {
	return !e.haveEncoded || e.forceFirstFrameAfterReset
}

// MarkEncoded marks that a frame has been successfully encoded.
func (e *Encoder) MarkEncoded() {
	e.haveEncoded = true
}

// ResetPacketState resets per-packet encoder state for standalone/shared encoding.
// This mirrors the standalone EncodeFrame() packet initialization.
func (e *Encoder) ResetPacketState() {
	e.nFramesEncoded = 0
	e.stereoCondMid = nil
	e.stereoChannelIdx = 0
	e.stereoPrevDecodeOnlyMiddle = 0
	e.forceFirstFrameAfterReset = e.reducedDependency
	e.setupLBRRForNewPacket()
}

// Bandwidth returns the current bandwidth setting.
func (e *Encoder) Bandwidth() Bandwidth {
	return e.bandwidth
}

// LPCOrder returns the LPC order for current bandwidth.
func (e *Encoder) LPCOrder() int {
	return int(e.lpcOrder)
}

// SampleRate returns the sample rate for current bandwidth.
func (e *Encoder) SampleRate() int {
	return int(e.sampleRate)
}

// LPMode returns the LP variable cutoff filter mode.
// 0 = idle, <0 = switching down, >0 = switching up.
func (e *Encoder) LPMode() int {
	return e.lpState.Mode
}

// GetLPState returns a copy of the LP variable cutoff filter state.
func (e *Encoder) GetLPState() LPState {
	return e.lpState
}

// SetLPState restores the LP variable cutoff filter state.
func (e *Encoder) SetLPState(state LPState) {
	e.lpState = state
}

// InWBModeWithoutVariableLP returns true when the SILK encoder is in
// wideband (16kHz) mode with the variable LP filter inactive (mode==0).
// This matches libopus silk_mode.inWBmodeWithoutVariableLP.
func (e *Encoder) InWBModeWithoutVariableLP() bool {
	return e.sampleRate == 16000 && e.lpState.Mode == 0
}

// AllowBandwidthSwitch reports the libopus SILK packet-level
// allowBandwidthSwitch gate used by Opus auto-bandwidth selection.
func (e *Encoder) AllowBandwidthSwitch() bool {
	return e.allowBandwidthSwitch
}

// PreviousLogGain returns the previous frame's log gain value.
func (e *Encoder) PreviousLogGain() int32 {
	return e.previousLogGain
}

// SetPreviousLogGain sets the log gain for delta coding.
func (e *Encoder) SetPreviousLogGain(gain int32) {
	e.previousLogGain = gain
}

// IsPreviousFrameVoiced returns whether the previous frame was voiced.
func (e *Encoder) IsPreviousFrameVoiced() bool {
	return e.isPreviousFrameVoiced
}

// SetPreviousFrameVoiced sets the voiced state for the previous frame.
func (e *Encoder) SetPreviousFrameVoiced(voiced bool) {
	e.isPreviousFrameVoiced = voiced
}

// SetVADState sets speech activity and spectral tilt from VAD analysis.
// These values influence pitch thresholding and noise shaping.
func (e *Encoder) SetVADState(speechActivityQ8 int32, inputTiltQ15 int32, inputQualityBandsQ15 [4]int32) {
	if speechActivityQ8 < 0 {
		speechActivityQ8 = 0
	}
	if speechActivityQ8 > 255 {
		speechActivityQ8 = 255
	}
	e.speechActivityQ8 = speechActivityQ8
	e.inputTiltQ15 = inputTiltQ15
	e.inputQualityBandsQ15 = inputQualityBandsQ15
	e.speechActivitySet = true
}

// LTPCorr returns the last pitch correlation estimate.
func (e *Encoder) LTPCorr() float32 {
	return e.ltpCorr
}

// PrevLSFQ15 returns the previous frame's LSF coefficients.
func (e *Encoder) PrevLSFQ15() []int16 {
	return e.prevLSFQ15
}

// SetPrevLSFQ15 copies LSF coefficients for interpolation with next frame.
func (e *Encoder) SetPrevLSFQ15(lsf []int16) {
	copy(e.prevLSFQ15, lsf)
}

// PrevStereoWeights returns the previous stereo weights.
func (e *Encoder) PrevStereoWeights() [2]int16 {
	return e.prevStereoWeights
}

// SetPrevStereoWeights sets the stereo weights for the next frame.
func (e *Encoder) SetPrevStereoWeights(weights [2]int16) {
	e.prevStereoWeights = weights
}

// InputBuffer returns the input sample buffer.
func (e *Encoder) InputBuffer() []float32 {
	return e.inputBuffer
}

// LPCState returns the LPC filter state.
func (e *Encoder) LPCState() []float32 {
	return e.lpcState
}

// FinalRange returns the final range coder state after encoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after EncodeFrame() to get a meaningful value.
func (e *Encoder) FinalRange() uint32 {
	return e.lastRng
}

// LastEncodedSignalInfo returns the signal type and quantization offset from
// the most recently encoded frame. Used by the hybrid encoder to provide
// silk_info feedback to CELT (libopus celt_encoder.c line 2466-2467).
//
// The offset is computed from silk_Quantization_Offsets_Q10[signalType>>1][quantOffsetType]:
//
//	Unvoiced Low:  100, Unvoiced High: 240
//	Voiced Low:     32, Voiced High:   100
func (e *Encoder) LastEncodedSignalInfo() (signalType, offset int) {
	signalType = int(e.ecPrevSignalType)
	return signalType, GetQuantizationOffset(signalType, e.lastQuantOffsetType)
}

// GetQuantizationOffset returns the quantization offset Q10 value for a given
// signal type and quantization offset type. This matches libopus
// silk_Quantization_Offsets_Q10[signalType>>1][quantOffsetType].
func GetQuantizationOffset(signalType, quantOffsetType int) int {
	return getQuantizationOffset(signalType, quantOffsetType)
}

// SetBitrate sets the target bitrate in bps. In stereo packets this is the
// total stream rate (matching libopus silk_EncControlStruct.bitRate); the
// stereo split into mid/side rates happens inside silk_stereo_LR_to_MS via
// stereoAllocationTargetRate, so callers must not pre-divide by channels.
func (e *Encoder) SetBitrate(bitrate int) {
	e.targetRateBps = int32(bitrate)
}

// SetPreAdjustedTargetRateBps provides a one-shot frame target that already
// includes packet-level reservoir/bits-balance adjustments. Shared stereo
// packet control uses this to avoid applying the same packet correction once
// per channel.
func (e *Encoder) SetPreAdjustedTargetRateBps(bitrate int) {
	e.preAdjustedTargetRateBps = int32(bitrate)
}

// UpdatePacketBitsExceeded applies libopus packet-level nBitsExceeded update.
// This must be called once per packet when shared range coding is used.
func (e *Encoder) UpdatePacketBitsExceeded(nBytesOut, payloadSizeMs, bitRateBps int) {
	if payloadSizeMs <= 0 {
		return
	}
	if bitRateBps > 0 {
		e.nBitsExceeded += int32(nBytesOut * 8)
		e.nBitsExceeded -= int32((bitRateBps * payloadSizeMs) / 1000)
		if e.nBitsExceeded < 0 {
			e.nBitsExceeded = 0
		} else if e.nBitsExceeded > 10000 {
			e.nBitsExceeded = 10000
		}
	}
	e.updateAllowBandwidthSwitch(payloadSizeMs)
}

// SetBitsExceeded sets packet-level bit reservoir excess state.
func (e *Encoder) SetBitsExceeded(bits int) {
	if bits < 0 {
		bits = 0
	} else if bits > 10000 {
		bits = 10000
	}
	e.nBitsExceeded = int32(bits)
}

// BitsExceeded returns packet-level bit reservoir excess state.
func (e *Encoder) BitsExceeded() int {
	return int(e.nBitsExceeded)
}

// SetMaxBits sets the maximum number of bits allowed for the current frame.
func (e *Encoder) SetMaxBits(maxBits int) {
	e.maxBits = int32(maxBits)
}

// SetVBR enables or disables variable bitrate mode.
func (e *Encoder) SetVBR(vbr bool) {
	e.useVBR = vbr
	e.useCBR = !vbr
	e.blockUseCBR = e.useCBR
}

// SetReducedDependency enables/disables reduced dependency coding.
// This mirrors libopus OPUS_SET_PREDICTION_DISABLED SILK behavior.
func (e *Encoder) SetReducedDependency(enabled bool) {
	e.reducedDependency = enabled
	if !enabled {
		e.forceFirstFrameAfterReset = false
	}
}

// ReducedDependency reports whether reduced dependency coding is enabled.
func (e *Encoder) ReducedDependency() bool {
	return e.reducedDependency
}

// SetFEC enables or disables in-band Forward Error Correction (LBRR).
// When enabled, each packet includes redundant data for the previous frame
// at a lower quality, allowing the decoder to recover from packet loss.
//
// Reference: libopus silk/control_codec.c silk_setup_LBRR
func (e *Encoder) SetFEC(enabled bool) {
	// silk_setup_LBRR: LBRR_in_previous_packet = psEncC->LBRR_enabled before update.
	e.lbrrPrevPacketHadLBRR = e.lbrrEnabled
	e.useFEC = enabled
	e.updateLBRREnabled()
}

// FECEnabled returns whether FEC (LBRR) is enabled.
func (e *Encoder) FECEnabled() bool {
	return e.useFEC
}

// SetPacketLoss sets the expected packet loss percentage (0-100).
// This affects the LBRR gain increase - higher loss means more aggressive
// redundancy encoding with higher gains.
//
// Reference: libopus silk/control_codec.c silk_setup_LBRR
func (e *Encoder) SetPacketLoss(lossPercent int) {
	if lossPercent < 0 {
		lossPercent = 0
	}
	if lossPercent > 100 {
		lossPercent = 100
	}
	e.packetLossPercent = int32(lossPercent)
	e.updateLBRREnabled()
}

// PacketLoss returns the configured packet loss percentage.
func (e *Encoder) PacketLoss() int {
	return int(e.packetLossPercent)
}

// updateLBRREnabled refreshes FEC intent without recomputing per-packet LBRR gain
// increases. Gain increases are configured in setupLBRRForNewPacket, matching
// libopus silk_setup_LBRR at packet boundaries (not on each SetFEC/SetPacketLoss).
func (e *Encoder) updateLBRREnabled() {
	e.lbrrEnabled = e.useFEC
}

// setupLBRRForNewPacket configures LBRR gain increases for the next packet.
// Matches libopus silk/control_codec.c silk_setup_LBRR.
func (e *Encoder) setupLBRRForNewPacket() {
	lbrrInPreviousPacket := e.lbrrPrevPacketHadLBRR
	e.lbrrEnabled = e.useFEC
	e.lbrrLTPRoundLoss = lbrrInPreviousPacket && e.useFEC
	if !e.lbrrEnabled {
		return
	}
	if !lbrrInPreviousPacket {
		e.lbrrGainIncreases = 7
		return
	}
	gainDecrease := (e.packetLossPercent * 13107) >> 16
	e.lbrrGainIncreases = max(7-gainDecrease, 3)
}

// finishLBRRPacket records whether this packet carried LBRR for the next packet's
// silk_setup_LBRR gain-increase selection (LBRR_in_previous_packet).
func (e *Encoder) finishLBRRPacket() {
	e.lbrrPrevPacketHadLBRR = e.lbrrEnabled
}

// LBRREnabled returns whether LBRR is currently active.
// LBRR may be disabled even when FEC is enabled if bitrate is too low.
func (e *Encoder) LBRREnabled() bool {
	return e.lbrrEnabled
}

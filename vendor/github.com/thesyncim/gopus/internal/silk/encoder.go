package silk

import "github.com/thesyncim/gopus/internal/rangecoding"

// Encoder is the state of one SILK encoder channel (silk_encoder_state_FLP,
// silk/float/structs_FLP.h): the input buffer the resampler fills, the VAD, the
// analysis and quantization history, and the LBRR data kept for the next
// packet. A PacketEncoder owns one Encoder per coded channel and drives it
// frame by frame (silk/enc_API.c); encodeFrame is the per-channel body
// (silk_encode_frame_FLP).
//
// Reference: RFC 6716 Section 5.2, draft-vos-silk-01
type Encoder struct {
	// silkEncoderFixedFields carries the FIXED_POINT integer SILK encode state
	// added under the gopus_fixed_point build. It is empty (zero-size) in the
	// default build.
	silkEncoderFixedFields

	// Range encoder the current frame is coded into.
	rangeEncoder *rangecoding.Encoder

	// Input buffering (sCmn.inputBuf / inputBufIx). The resampler writes the
	// frame at inputBuf[2:]; the VAD and the frame encoder read inputBuf[1:].
	inputBuf   [maxFrameLength + 2]int16
	inputBufIx int32
	resampler  *LibopusResampler // API rate -> internal rate (sCmn.resampler_state)

	// Sampling rates (silk_control_encoder): the API rate of the input, the
	// API rate the resampler was set up for, and the limits and the request for
	// the internal rate.
	apiFsHz             int32 // sCmn.API_fs_Hz
	prevAPIFsHz         int32 // sCmn.prev_API_fs_Hz
	maxInternalFsHz     int32 // sCmn.maxInternal_fs_Hz
	minInternalFsHz     int32 // sCmn.minInternal_fs_Hz
	desiredInternalFsHz int32 // sCmn.desiredInternal_fs_Hz
	// allowBandwidthSwitch is sCmn.allow_bandwidth_switch, the packet encoder's
	// flag handed to silk_control_audio_bandwidth.
	allowBandwidthSwitch bool

	// silk_setup_resamplers scratch: x_buf as int16 at the internal rate and
	// at the API rate, and the resampler taking x_buf back to the API rate.
	xBufFix         []int16
	xBufAPI         []int16
	xBufAPIResample LibopusResampler

	// Voice activity detection (sCmn.sVAD and the silk_encode_do_VAD_FLP outputs).
	vad             silkVADState
	vadScratch      []int16
	vadFlags        [maxFramesPerPacket]bool // sCmn.VAD_flags
	noSpeechCounter int32
	inDTX           bool
	useDTX          bool
	useCBR          bool // sCmn.useCBR, read by the noise shaping analysis

	// Packet configuration set by the per-packet control (silk_control_encoder).
	packetSizeMs               int32
	nbSubfr                    int32
	frameLength                int32
	firstFrameAfterReset       bool
	prefillFlag                bool
	controlledSinceLastPayload bool

	// Frame state (persists across frames, mirrors decoder)
	previousGainIndex     int8  // Previous gain quantization index [0, 63] (libopus sShape.LastGainIndex)
	isPreviousFrameVoiced bool  // Was previous frame voiced
	variableHPSmth1Q15    int32 // Smoothed log-domain HP cutoff estimate (silk_HP_variable_cutoff)
	ecPrevLagIndex        int16 // Previous lag index for conditional pitch coding
	ecPrevSignalType      int32 // Previous signal type for conditional pitch coding
	lastQuantOffsetType   int   // Last frame's quantization offset type (for hybrid silk_info)
	lastSeed              int8  // Last frame's encoded random seed
	frameCounter          int32 // Frame counter for seed generation (seed = frameCounter & 3)

	// LPC state
	lpcOrder   int32              // sCmn.predictLPCOrder (10 for NB/MB, 16 for WB)
	prevLSFQ15 [maxLPCOrder]int16 // sCmn.prev_NLSFq_Q15, the previous quantized NLSFs

	// LP variable cutoff filter state (for smooth bandwidth transitions)
	lpState LPState

	// Pitch analysis state
	pitchState PitchAnalysisState // State for pitch estimation across frames

	// VAD-derived state for the current frame (sCmn.speech_activity_Q8,
	// input_tilt_Q15, input_quality_bands_Q15).
	speechActivityQ8     int32    // Speech activity in Q8 (0-255)
	inputTiltQ15         int32    // Spectral tilt in Q15 from VAD
	inputQualityBandsQ15 [4]int32 // Quality in each VAD band (Q15)

	// NSQ (Noise Shaping Quantization) state
	nsqState        *NSQState        // Noise shaping quantizer state for proper libopus-matching
	lbrrNSQState    NSQState         // copy of nsqState the LBRR quantization runs on
	noiseShapeState *NoiseShapeState // Noise shaping analysis state for adaptive parameters

	// Encoder control parameters (persists across frames)
	targetRateBps          int32   // sCmn.TargetRate_bps, the rate silk_control_SNR last mapped
	snrDBQ7                int32   // Target SNR in dB (Q7 format, e.g., 25 dB = 25 * 128)
	ltpCorr                float32 // LTP correlation from pitch analysis [0, 1]
	sumLogGainQ7           int32   // Sum log gain for LTP quantization
	complexity             int32   // Encoder complexity (0-10)
	nStatesDelayedDecision int32   // Delayed decision states (libopus control_codec)

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

	// x_buf: LTP memory, the frame being encoded and the noise shaping
	// lookahead, in silk_float normalized to [-1, 1].
	xBuf []float32

	// Internal sampling rate (sCmn.fs_kHz, 0 until the first
	// silk_control_encoder) and the bandwidth it codes.
	fsKHz     int32
	bandwidth Bandwidth

	// FEC/LBRR (Low Bitrate Redundancy) state
	// LBRR provides forward error correction by encoding redundant data
	// for the previous frame at a lower quality in the current packet.
	// Reference: libopus silk/structs.h silk_encoder_state
	lbrrEnabled         bool                                // LBRR_enabled for the current packet (silk_setup_LBRR)
	lbrrGainIncreases   int32                               // Gain increase for LBRR encoding
	lbrrPrevLastGainIdx int8                                // Previous frame's last gain index for LBRR
	lbrrFlags           [maxFramesPerPacket]int32           // LBRR flags per frame in packet
	lbrrFlag            int8                                // LBRR flag for current packet header
	lbrrIndices         [maxFramesPerPacket]sideInfoIndices // LBRR indices per frame
	lbrrPulses          [maxFramesPerPacket][]int8          // LBRR pulses per frame
	lbrrFrameLength     [maxFramesPerPacket]int32           // LBRR frame length per frame
	lbrrNbSubfr         [maxFramesPerPacket]int32           // LBRR subframe count per frame
	packetLossPercent   int32                               // Expected packet loss (0-100)
	nFramesEncoded      int32                               // Number of frames encoded in current packet
	nFramesPerPacket    int32                               // Number of frames per packet

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
	// nsqLowerCopy is sNSQ_copy[1] of silk_encode_frame_FLP: the NSQ state of
	// the best under-budget gain pass, written only when that pass is found.
	nsqLowerCopy NSQState

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

	// frameF32 holds the LP-filtered frame as silk_float normalized to [-1, 1].
	frameF32 []float32
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

// newEncoder creates one encoder channel in the state silk_init_encoder
// (silk/init_encoder.c) leaves it: it has no internal sampling rate until the
// first silk_control_encoder picks one. The buffers are sized for the highest
// internal rate, so a rate change reuses them.
func newEncoder() *Encoder {
	lbrrPulses := [maxFramesPerPacket][]int8{}
	for i := range lbrrPulses {
		lbrrPulses[i] = make([]int8, maxFrameLength)
	}
	enc := &Encoder{
		resampler: &LibopusResampler{},
		// x_buf: LTP memory, noise shaping lookahead and one frame.
		xBuf:            make([]float32, (ltpMemLengthMs+laShapeMs)*maxFsKHz+maxFrameLength),
		nsqState:        NewNSQState(),
		noiseShapeState: NewNoiseShapeState(),
		// Pitch residual: LTP memory, one frame and the pitch lookahead.
		scratchPitchRes32: make([]float32, (ltpMemLengthMs+laPitchMs)*maxFsKHz+maxFrameLength),
		lbrrPulses:        lbrrPulses,
	}
	enc.reset()
	return enc
}

// reset returns the channel to the state silk_init_encoder
// (silk/init_encoder.c) leaves it, reusing the allocated buffers: every
// analysis, quantization, VAD and LBRR history is cleared, no internal
// sampling rate is set and the next frame is the first after a reset. The next
// silk_control_encoder re-initializes the resampler.
func (e *Encoder) reset() {
	e.resetFixedState()
	e.inputBuf = [maxFrameLength + 2]int16{}
	e.inputBufIx = 0
	e.apiFsHz = 0
	e.prevAPIFsHz = 0
	e.maxInternalFsHz = 0
	e.minInternalFsHz = 0
	e.desiredInternalFsHz = 0
	e.allowBandwidthSwitch = false
	silkVADInit(&e.vad)
	e.vadFlags = [maxFramesPerPacket]bool{}
	e.noSpeechCounter = 0
	e.inDTX = false
	e.useDTX = false
	e.useCBR = false
	e.packetSizeMs = 0
	e.nbSubfr = 0
	e.frameLength = 0
	e.firstFrameAfterReset = true
	e.prefillFlag = false
	e.controlledSinceLastPayload = false

	e.previousGainIndex = 0
	e.isPreviousFrameVoiced = false
	e.variableHPSmth1Q15 = initVariableHPSmth1Q15()
	e.ecPrevLagIndex = 0
	e.ecPrevSignalType = typeNoVoiceActivity
	e.lastQuantOffsetType = 0
	e.lastSeed = 0
	e.frameCounter = 0
	e.lpcOrder = 0
	e.prevLSFQ15 = [maxLPCOrder]int16{}
	e.lpState = LPState{}
	e.pitchState = PitchAnalysisState{}
	e.speechActivityQ8 = 0
	e.inputTiltQ15 = 0
	e.inputQualityBandsQ15 = [4]int32{}
	e.nsqState.Reset()
	e.noiseShapeState.Reset()
	e.targetRateBps = 0
	e.snrDBQ7 = 0
	e.ltpCorr = 0
	e.sumLogGainQ7 = 0
	e.lastTotalEnergy = 0
	e.lastInvGain = 0
	e.lastLPCGain = 0
	e.lastNumSamples = 0
	clear(e.xBuf)
	e.fsKHz = 0
	e.bandwidth = BandwidthNarrowband

	e.lbrrEnabled = false
	e.lbrrGainIncreases = 0
	e.lbrrPrevLastGainIdx = 0
	e.lbrrFlags = [maxFramesPerPacket]int32{}
	e.lbrrFlag = 0
	e.lbrrIndices = [maxFramesPerPacket]sideInfoIndices{}
	for i := range e.lbrrPulses {
		clear(e.lbrrPulses[i])
	}
	e.lbrrFrameLength = [maxFramesPerPacket]int32{}
	e.lbrrNbSubfr = [maxFramesPerPacket]int32{}
	e.packetLossPercent = 0
	e.nFramesEncoded = 0
	e.nFramesPerPacket = 0
}

// resetAnalysisHistory clears the state silk_setup_fs (silk/control_codec.c)
// resets when the internal sampling rate changes, and silk_Encode
// (silk/enc_API.c) resets on the side channel when stereo side coding resumes
// after mid-only frames: the noise shaping and quantizer state, the previous
// NLSFs and the LP filter memory. The pitch lag and gain history restart at
// their initial values and the next frame is coded as the first after a reset.
func (e *Encoder) resetAnalysisHistory() {
	e.noiseShapeState.Reset()
	e.nsqState.Reset()
	e.nsqState.lagPrev = 100
	e.nsqState.prevGainQ16 = 1 << 16
	e.prevLSFQ15 = [maxLPCOrder]int16{}
	e.lpState.InLPState = [2]int32{}
	e.pitchState.prevLag = 100
	e.previousGainIndex = 10
	e.isPreviousFrameVoiced = false
	e.firstFrameAfterReset = true
	// The gopus_fixed_point build keeps the same history in its integer state.
	e.resetFixedAnalysisHistory()
}

// lastEncodedSignalInfo returns the signal type and quantization offset of the
// most recently encoded frame (sCmn.indices.signalType and
// silk_Quantization_Offsets_Q10[signalType>>1][quantOffsetType]).
func (e *Encoder) lastEncodedSignalInfo() (signalType, offset int32) {
	signalType = e.ecPrevSignalType
	return signalType, int32(getQuantizationOffset(int(signalType), e.lastQuantOffsetType))
}

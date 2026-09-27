package silk

import (
	"github.com/thesyncim/gopus/internal/arena"
	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// Decoder decodes SILK frames from an Opus packet.
// It maintains state across frames for proper speech continuity.
//
// SILK is the speech layer of Opus, using Linear Predictive Coding (LPC)
// for efficient speech compression. The decoder reconstructs audio by:
// 1. Parsing frame headers (VAD, signal type, quantization offset)
// 2. Decoding parameters (gains, LSF/LPC coefficients, pitch lags)
// 3. Reconstructing excitation signal
// 4. Applying LTP (voiced) and LPC synthesis filters
//
// Reference: RFC 6716 Section 4.2
type Decoder struct {
	// Range decoder reference (set per frame)
	rangeDecoder  *rangecoding.Decoder
	apiSampleRate int

	// Frame state (persists across frames)
	haveDecoded           bool  // True after first frame decoded
	previousLogGain       int32 // Last subframe gain (for delta coding)
	isPreviousFrameVoiced bool  // Was previous frame voiced (for LTP)

	// LPC state (persists across frames)
	lpcOrder      int32     // Current LPC order (10 for NB/MB, 16 for WB)
	prevLPCValues []float32 // d_LPC output history for filter continuity

	// LSF state (persists for interpolation)
	prevLSFQ15 []int16 // Previous frame LSF coefficients (Q15)

	// Excitation/output history (for LTP lookback)
	// Needs at least max_pitch_lag + LTP_taps/2 + margin samples
	outputHistory []float32 // Ring buffer for pitch prediction
	historyIndex  int       // Current write position in ring buffer

	// Stereo state (for stereo unmixing)
	prevStereoWeights [2]int16 // Previous w0, w1 stereo weights (Q13)

	// libopus-aligned decoder state
	state                [2]decoderState
	stereo               stereoDecState
	prevDecodeOnlyMiddle int32

	// Track previous bandwidth to detect bandwidth changes.
	// Used to reset sMid state when sample rate changes.
	prevBandwidth    Bandwidth
	hasPrevBandwidth bool

	// Resamplers for each bandwidth (created on demand).
	// Separate resampler state per channel to match libopus.
	resamplers map[Bandwidth]*resamplerPair

	// Per-decoder PLC state (do not share across decoder instances).
	plcState *plc.State

	// Per-channel SILK PLC state (libopus-style LTP/LPC concealment inputs).
	silkPLCState [2]*plc.SILKPLCState

	// Mono output delay buffer to match libopus behavior.
	// libopus delays mono SILK output by (1 + inputDelay) samples:
	// - 1 sample from sMid history prepended before resampler input
	// - inputDelay samples from resampler delay buffer (4 for 8kHz)
	monoDelayBuf     []int16 // Delay buffer (size = fsKHz)
	monoInputDelay   int     // Delay compensation (from delay_matrix_dec)
	monoDelayBufInit bool    // Whether delay buffer has been initialized

	// Pre-allocated scratch buffers for hot-path performance.
	// These eliminate allocations in performance-critical decode loops.
	// Sizes are based on maximum SILK frame parameters:
	// - maxSubFrameLength = 80 (5ms * 16kHz)
	// - maxLPCOrder = 16
	// - maxLtpMemLength = 320 (20ms * 16kHz)
	// - maxFrameLength = 320 (4 subframes * 80 samples)
	// - maxFramesPerPacket = 3
	//
	// The fixed-size scratch slices below are carved from three per-type arenas
	// (one allocation each instead of ~27 separate buffers) in NewDecoder; see
	// the scratch carve there. They are pure per-frame scratch (overwritten before
	// read), so sharing a backing per type is bit-exact.
	scratchI16 arena.Bump[int16]
	scratchI32 arena.Bump[int32]
	scratchF32 arena.Bump[float32]

	scratchSLPC     []int32   // Size: maxSubFrameLength + maxLPCOrder = 96
	scratchSLTP     []int16   // Size: maxLtpMemLength = 320
	scratchSLTPQ15  []int32   // Size: maxLtpMemLength + maxFrameLength = 640
	scratchPresQ14  []int32   // Size: maxSubFrameLength = 80
	scratchOutInt16 []int16   // Size: maxFramesPerPacket * maxFrameLength = 960
	scratchPulses   []int16   // Size: roundUpShellFrame(maxFrameLength) = 320
	scratchOutput   []float32 // Size: maxFramesPerPacket * maxFrameLength = 960
	// scratchFECOut is the multi-frame output accumulator for DecodeFEC. It is
	// kept distinct from scratchOutInt16 because a no-LBRR sub-frame in an FEC
	// packet runs concealment (recordPLCLossForState) that writes through
	// scratchOutInt16; sharing the buffer would corrupt earlier sub-frames'
	// already-decoded LBRR output.
	scratchFECOut []int16 // Size: maxFramesPerPacket * maxFrameLength = 960

	// Scratch buffers for silkDecodeIndices
	scratchEcIx   []int16 // Size: maxLPCOrder = 16
	scratchPredQ8 []uint8 // Size: maxLPCOrder = 16

	// Scratch buffers for silkDecodePulses
	// iter = frameLength >> 4 + 1 = 320/16 + 1 = 21 max
	scratchSumPulses []int32 // Size: 21
	scratchNLshifts  []int32 // Size: 21

	// Scratch buffers for resampler - eliminate allocations in Process()
	resamplerScratchIn     []int16   // Size: max input samples (fsInKHz * 10 = 160)
	resamplerScratchOut    []int16   // Size: max output samples (480 for 48kHz)
	resamplerScratchResult []float32 // Size: max output samples (480)
	resamplerScratchBuf    []int16   // Size: 2*batchSize + resamplerOrderFIR12

	// Scratch buffer for upsampleTo48k
	upsampleScratch []float32 // Size: maxFramesPerPacket * maxFrameLength * 6 = 5760

	dredHookState

	nativePostfilter nativePostfilterExtras

	// Scratch buffers for applyMonoDelay
	monoResamplerIn []int16 // Size: maxFramesPerPacket * maxFrameLength = 960
	monoOutput      []int16 // Size: maxFramesPerPacket * maxFrameLength = 960

	// Scratch buffer for BuildMonoResamplerInput
	buildMonoInputScratch []float32 // Size: maxFramesPerPacket * maxFrameLength = 960

	// Scratch buffers for stereo SILK decode paths.
	stereoLeftNative  []int16 // Size: maxFramesPerPacket * maxFrameLength = 960
	stereoRightNative []int16 // Size: maxFramesPerPacket * maxFrameLength = 960
	stereoMidNative   []int16 // Size: maxFramesPerPacket * maxFrameLength = 960
	stereoMidFrame    []int16 // Size: maxFrameLength + 2
	stereoSideFrame   []int16 // Size: maxFrameLength + 2

	// Scratch buffers for stereo SILK packet-loss concealment (decodePLCStereo).
	// These mirror the stereo good-frame scratch so PLC stays allocation-free.
	plcMidNative  []float32 // concealed mid at native rate
	plcSideNative []float32 // concealed side at native rate
	plcLeftUp     []float32 // resampled left at API rate
	plcRightUp    []float32 // resampled right at API rate
	plcPredQ13    [2]int32  // stereo predictor coefficients for MS->LR

	// Cached per-channel PLC views passed into the concealment kernel; created
	// lazily and reused so packet-loss concealment stays allocation-free.
	plcViews [2]*silkPLCChannelView

	// Length of the most recent native-rate int16 mono decode written to
	// scratchOutInt16. Exposed via LatestNativeMono so optional decoder-side
	// post-processing (e.g. OSCE BWE) can read the pre-resample SILK output
	// without performing a second decode pass.
	lastNativeMonoLen int32
	// Native SILK sample rate in kHz used to produce lastNativeMonoLen samples
	// (e.g. 16 for WB). Zero until a decode has run.
	lastNativeMonoFsKHz int32

	// Length of the most recent native-rate int16 stereo decode written to
	// stereoLeftNative / stereoRightNative. Exposed via LatestNativeStereo
	// so the optional OSCE BWE forward pass can read both pre-resample SILK
	// lowband channels (libopus runs the BWE forward pass per channel with
	// its own BBWENet state). Zero until a stereo decode has run.
	lastNativeStereoLen int32
	// Native SILK sample rate in kHz used to produce lastNativeStereoLen
	// samples (e.g. 16 for WB). Zero until a stereo decode has run.
	lastNativeStereoFsKHz int32

	// Length and rate for the most recent internal stereo mid channel before
	// MS->LR conversion. libopus feeds decoder-side DeepPLC/DRED from SILK
	// channel 0 only, so parity paths need this native mid channel rather than
	// post-stereo left/right output.
	lastNativeMidLen   int32
	lastNativeMidFsKHz int32

	// lastFrameCtrl caches the most recent silk_decoder_control produced by
	// `decodeFrameCoreInto` for each channel. The OSCE LACE / NoLACE
	// postfilter feature extractor reads PredCoef_Q12, LTPCoef_Q14,
	// Gains_Q16 and pitchL out of this cache after SILK decode completes
	// (libopus does the equivalent by reading the live psDecCtrl from the
	// stack frame). Both fields are only valid for the channel/decoded
	// frames; multi-frame packets retain only the last 20 ms frame's ctrl
	// (matching the LACE/NoLACE per-frame cadence which only runs at fs=16).
	lastFrameCtrl       [2]decoderControl
	lastFrameCtrlSignal [2]int32 // signalType from st.indices for the corresponding ctrl.
	lastFrameCtrlValid  [2]bool

	// plcLowbandCapture, when non-nil, receives the resampled int16 SILK PLC
	// lowband (interleaved by channel) produced by the next decodePLC /
	// decodePLCStereo call. The FIXED_POINT integer hybrid PLC path arms it so
	// the integer CELT highband concealment can accumulate onto the exact
	// opus_res SILK lowband (INT16TORES of this int16), matching libopus'
	// celt_decode_with_ec_dred(NULL, celt_accum=1) on a lost hybrid frame. The
	// captured int16 is the same value the float PLC resamples to (the float
	// output is int16/32768), so arming it does not change the float PLC output.
	plcLowbandCapture    []int16
	plcLowbandCaptured   int
	plcLowbandCaptureArm bool
	// plcConcealI16 is reusable scratch for converting a mono float PLC
	// concealment frame to int16 before int16 resampling on the capture path.
	plcConcealI16 []int16
	// plcStereoLI16 / plcStereoRI16 are reusable per-channel int16 scratch for
	// the stereo PLC lowband capture path.
	plcStereoLI16 []int16
	plcStereoRI16 []int16
}

// ArmPLCLowbandCapture arms (or, with buf==nil, disarms) capture of the next
// decodePLC / decodePLCStereo call's resampled int16 SILK lowband into buf
// (interleaved by channel, length frameSize*channels). After the PLC decode the
// caller reads PLCLowbandCaptured for the number of interleaved samples filled.
// It is used only by the FIXED_POINT integer hybrid PLC path.
func (d *Decoder) ArmPLCLowbandCapture(buf []int16) {
	d.plcLowbandCapture = buf
	d.plcLowbandCaptured = 0
	d.plcLowbandCaptureArm = buf != nil
}

// PLCLowbandCaptured returns the number of interleaved int16 samples written to
// the capture buffer armed via ArmPLCLowbandCapture by the most recent PLC decode.
func (d *Decoder) PLCLowbandCaptured() int { return d.plcLowbandCaptured }

// NewDecoder creates a new SILK decoder with proper initial state.
// The decoder is ready to process SILK frames after creation.
func NewDecoder() *Decoder {
	// Pre-calculate max buffer sizes based on SILK constants:
	// - maxSubFrameLength = 80 (5ms * 16kHz)
	// - maxLPCOrder = 16
	// - maxLtpMemLength = 320 (20ms * 16kHz)
	// - maxFrameLength = 320 (4 subframes * 80)
	// - maxFramesPerPacket = 3
	const (
		maxSLPCSize     = 80 + 16   // maxSubFrameLength + maxLPCOrder
		maxSLTPSize     = 320       // maxLtpMemLength
		maxSLTPQ15Size  = 320 + 320 // maxLtpMemLength + maxFrameLength
		maxPresQ14Size  = 80        // maxSubFrameLength
		maxOutInt16Size = 3 * 320   // maxFramesPerPacket * maxFrameLength
		maxPulsesSize   = 320       // roundUpShellFrame(maxFrameLength)
		maxOutputSize   = 3 * 320   // maxFramesPerPacket * maxFrameLength

		// Additional scratch buffer sizes
		maxIterSize     = 21   // (maxFrameLength >> 4) + 1
		maxResamplerIn  = 160  // 16kHz * 10ms = 160 samples
		maxResamplerOut = 480  // 48kHz * 10ms = 480 samples
		maxResamplerBuf = 328  // 2 * 160 + 8 = 328 (2*batchSize + resamplerOrderFIR12)
		maxUpsampleSize = 5760 // 3 * 320 * 6 = 5760 (maxFramesPerPacket * maxFrameLength * 6x upsample)
	)

	d := &Decoder{
		apiSampleRate: 48000,
		prevLPCValues: make([]float32, 16),  // Max for WB (d_LPC = 16)
		prevLSFQ15:    make([]int16, 16),    // Max for WB (d_LPC = 16)
		outputHistory: make([]float32, 322), // Max pitch lag (288) + LTP taps (5) + margin

		// scratchPredQ8 is the only fixed scratch kept standalone (uint8); the rest
		// are carved from the per-type arenas below.
		scratchPredQ8: make([]uint8, maxLPCOrder),
		plcState:      plc.NewState(),
	}
	// Carve the fixed-size scratch into one arena per element type (a single
	// allocation each, in one contiguous cache region, instead of ~27 separate
	// buffers). All of these are pure per-frame scratch — overwritten before read —
	// so backing them from a shared per-type arena is bit-exact.
	d.scratchI16.Ensure(maxSLTPSize + maxPulsesSize + maxLPCOrder + maxResamplerIn +
		maxResamplerOut + maxResamplerBuf + 6*maxOutInt16Size + 2*(maxFrameLength+2))
	d.scratchSLTP = d.scratchI16.AllocN(maxSLTPSize)
	d.scratchOutInt16 = d.scratchI16.AllocN(maxOutInt16Size)
	d.scratchFECOut = d.scratchI16.AllocN(maxOutInt16Size)
	d.scratchPulses = d.scratchI16.AllocN(maxPulsesSize)
	d.scratchEcIx = d.scratchI16.AllocN(maxLPCOrder)
	d.resamplerScratchIn = d.scratchI16.AllocN(maxResamplerIn)
	d.resamplerScratchOut = d.scratchI16.AllocN(maxResamplerOut)
	d.resamplerScratchBuf = d.scratchI16.AllocN(maxResamplerBuf)
	d.monoResamplerIn = d.scratchI16.AllocN(maxOutInt16Size)
	d.monoOutput = d.scratchI16.AllocN(maxOutInt16Size)
	d.stereoLeftNative = d.scratchI16.AllocN(maxOutInt16Size)
	d.stereoRightNative = d.scratchI16.AllocN(maxOutInt16Size)
	d.stereoMidFrame = d.scratchI16.AllocN(maxFrameLength + 2)
	d.stereoSideFrame = d.scratchI16.AllocN(maxFrameLength + 2)

	d.scratchI32.Ensure(maxSLPCSize + maxSLTPQ15Size + maxPresQ14Size + 2*maxIterSize)
	d.scratchSLPC = d.scratchI32.AllocN(maxSLPCSize)
	d.scratchSLTPQ15 = d.scratchI32.AllocN(maxSLTPQ15Size)
	d.scratchPresQ14 = d.scratchI32.AllocN(maxPresQ14Size)
	d.scratchSumPulses = d.scratchI32.AllocN(maxIterSize)
	d.scratchNLshifts = d.scratchI32.AllocN(maxIterSize)

	d.scratchF32.Ensure(maxOutputSize + maxResamplerOut + maxUpsampleSize + 5*maxOutInt16Size)
	d.scratchOutput = d.scratchF32.AllocN(maxOutputSize)
	d.resamplerScratchResult = d.scratchF32.AllocN(maxResamplerOut)
	d.upsampleScratch = d.scratchF32.AllocN(maxUpsampleSize)
	d.buildMonoInputScratch = d.scratchF32.AllocN(maxOutInt16Size)
	d.plcMidNative = d.scratchF32.AllocN(maxOutInt16Size)
	d.plcSideNative = d.scratchF32.AllocN(maxOutInt16Size)
	d.plcLeftUp = d.scratchF32.AllocN(maxOutInt16Size)
	d.plcRightUp = d.scratchF32.AllocN(maxOutInt16Size)
	if nativeLowbandCaptureEnabled {
		d.stereoMidNative = make([]int16, maxOutInt16Size)
	}
	resetDecoderState(&d.state[0])
	resetDecoderState(&d.state[1])

	// Wire up scratch buffers to decoderState for hot-path optimization
	d.setupScratchBuffers()

	return d
}

// SetAPISampleRate sets the decoder output rate used by the libopus SILK
// resampler. It must be called before decoding any packets.
func (d *Decoder) SetAPISampleRate(sampleRate int) {
	switch sampleRate {
	case 8000, 12000, 16000, 24000, 48000:
		d.apiSampleRate = sampleRate
	default:
		d.apiSampleRate = 48000
	}
	d.resamplers = nil
}

func (d *Decoder) outputSampleRate() int {
	if d.apiSampleRate == 0 {
		return 48000
	}
	return d.apiSampleRate
}

func (d *Decoder) frameDurationFromAPISamples(samples int) FrameDuration {
	return FrameDurationFromSamples(samples, d.outputSampleRate())
}

// setupScratchBuffers wires the pre-allocated scratch buffers to both decoderState instances.
// This enables silkDecodeCore and related functions to use pre-allocated memory.
func (d *Decoder) setupScratchBuffers() {
	d.state[0].scratchSLPC = d.scratchSLPC
	d.state[0].scratchSLTP = d.scratchSLTP
	d.state[0].scratchSLTPQ15 = d.scratchSLTPQ15
	d.state[0].scratchPresQ14 = d.scratchPresQ14
	d.state[0].scratchEcIx = d.scratchEcIx
	d.state[0].scratchPredQ8 = d.scratchPredQ8
	d.state[0].scratchSumPulses = d.scratchSumPulses
	d.state[0].scratchNLshifts = d.scratchNLshifts

	d.state[1].scratchSLPC = d.scratchSLPC
	d.state[1].scratchSLTP = d.scratchSLTP
	d.state[1].scratchSLTPQ15 = d.scratchSLTPQ15
	d.state[1].scratchPresQ14 = d.scratchPresQ14
	d.state[1].scratchEcIx = d.scratchEcIx
	d.state[1].scratchPredQ8 = d.scratchPredQ8
	d.state[1].scratchSumPulses = d.scratchSumPulses
	d.state[1].scratchNLshifts = d.scratchNLshifts
}

// Reset clears decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	d.haveDecoded = false
	d.previousLogGain = 0
	d.isPreviousFrameVoiced = false
	d.lpcOrder = 0

	// Clear LPC history
	for i := range d.prevLPCValues {
		d.prevLPCValues[i] = 0
	}

	// Clear LSF history
	for i := range d.prevLSFQ15 {
		d.prevLSFQ15[i] = 0
	}

	// Clear output history
	for i := range d.outputHistory {
		d.outputHistory[i] = 0
	}
	d.historyIndex = 0

	// Clear stereo state
	d.prevStereoWeights = [2]int16{0, 0}

	resetDecoderState(&d.state[0])
	resetDecoderState(&d.state[1])
	d.stereo = stereoDecState{}
	d.prevDecodeOnlyMiddle = 0

	// Reset resampler state for a clean stream start
	for _, pair := range d.resamplers {
		if pair == nil {
			continue
		}
		if pair.left != nil {
			pair.left.Reset()
		}
		if pair.right != nil {
			pair.right.Reset()
		}
	}

	// Reset mono delay buffer state
	d.monoDelayBuf = nil
	d.monoInputDelay = 0
	d.monoDelayBufInit = false

	// Clear scratch buffers (zero them for clean state).
	// Note: We don't reallocate - the buffers remain allocated
	// and are reused across stream resets for performance.
	for i := range d.scratchSLPC {
		d.scratchSLPC[i] = 0
	}
	for i := range d.scratchSLTP {
		d.scratchSLTP[i] = 0
	}
	for i := range d.scratchSLTPQ15 {
		d.scratchSLTPQ15[i] = 0
	}
	for i := range d.scratchPresQ14 {
		d.scratchPresQ14[i] = 0
	}
	for i := range d.scratchOutInt16 {
		d.scratchOutInt16[i] = 0
	}
	for i := range d.scratchFECOut {
		d.scratchFECOut[i] = 0
	}
	if nativeLowbandCaptureEnabled {
		d.lastNativeMonoLen = 0
		d.lastNativeMonoFsKHz = 0
		d.lastNativeStereoLen = 0
		d.lastNativeStereoFsKHz = 0
		d.lastNativeMidLen = 0
		d.lastNativeMidFsKHz = 0
	}
	for c := range d.lastFrameCtrl {
		d.lastFrameCtrl[c] = decoderControl{}
		d.lastFrameCtrlSignal[c] = 0
		d.lastFrameCtrlValid[c] = false
	}
	for i := range d.scratchPulses {
		d.scratchPulses[i] = 0
	}
	for i := range d.scratchOutput {
		d.scratchOutput[i] = 0
	}
	for i := range d.scratchEcIx {
		d.scratchEcIx[i] = 0
	}
	for i := range d.scratchPredQ8 {
		d.scratchPredQ8[i] = 0
	}
	for i := range d.scratchSumPulses {
		d.scratchSumPulses[i] = 0
	}
	for i := range d.scratchNLshifts {
		d.scratchNLshifts[i] = 0
	}
	for i := range d.resamplerScratchIn {
		d.resamplerScratchIn[i] = 0
	}
	for i := range d.resamplerScratchOut {
		d.resamplerScratchOut[i] = 0
	}
	for i := range d.resamplerScratchResult {
		d.resamplerScratchResult[i] = 0
	}
	for i := range d.resamplerScratchBuf {
		d.resamplerScratchBuf[i] = 0
	}
	for i := range d.upsampleScratch {
		d.upsampleScratch[i] = 0
	}
	for i := range d.monoResamplerIn {
		d.monoResamplerIn[i] = 0
	}
	for i := range d.monoOutput {
		d.monoOutput[i] = 0
	}
	for i := range d.buildMonoInputScratch {
		d.buildMonoInputScratch[i] = 0
	}
	for i := range d.stereoLeftNative {
		d.stereoLeftNative[i] = 0
	}
	for i := range d.stereoRightNative {
		d.stereoRightNative[i] = 0
	}
	for i := range d.stereoMidFrame {
		d.stereoMidFrame[i] = 0
	}
	for i := range d.stereoSideFrame {
		d.stereoSideFrame[i] = 0
	}

	// Re-wire scratch buffers after resetDecoderState cleared them
	d.setupScratchBuffers()

	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	d.plcState.Reset()

	for ch := range d.silkPLCState {
		if d.silkPLCState[ch] != nil {
			d.silkPLCState[ch].Reset(int(d.state[ch].frameLength))
		}
	}
}

// applyMonoDelay applies libopus-compatible delay compensation for mono SILK output.
// This simulates the delay introduced by libopus's sMid buffering and resampler.
//
// The total delay is (1 + inputDelay) samples:
// - 1 sample from sMid[1] prepended before resampler input
// - inputDelay samples from resampler delay buffer (from delay_matrix_dec)
//
// For the same-rate "copy" resampler case, the delay values are:
// - 8kHz: inputDelay=4, total delay=5 samples
// - 12kHz: inputDelay=9, total delay=10 samples (but 8kHz->12kHz uses different path)
// - 16kHz: inputDelay=12, total delay=13 samples
//
// For native rate output (API rate = internal rate), we only need the
// delay from delay_matrix_dec[rateID(in)][rateID(in)].
func (d *Decoder) applyMonoDelay(decoded []int16, fsKHz int) []int16 {
	// Get inputDelay from delay_matrix_dec for native rate output
	// libopus uses rateID to convert Hz to index: 8->0, 12->1, 16->2
	var inputDelay int
	switch fsKHz {
	case 8:
		inputDelay = 4 // delay_matrix_dec[0][0] for 8kHz->8kHz API
	case 12:
		inputDelay = 9 // delay_matrix_dec[1][1] for 12kHz->12kHz API
	case 16:
		inputDelay = 12 // delay_matrix_dec[2][2] for 16kHz->16kHz API
	default:
		// Unknown rate, return as-is
		return decoded
	}

	// Initialize delay buffer if needed
	if !d.monoDelayBufInit || d.monoInputDelay != inputDelay {
		d.monoDelayBuf = make([]int16, fsKHz) // Delay buffer size = fsKHz samples (1ms)
		d.monoInputDelay = inputDelay
		d.monoDelayBufInit = true
	}

	// Build the resampler input: [sMid[1], decoded[0:n-1]]
	// libopus calls resampler with &samplesOut1_tmp[n][1] and nSamplesOutDec
	// This means the input has sMid[1] at position 0, then decoded[0:nSamplesOutDec-1]
	// The last decoded sample is NOT in the resampler input.
	nSamplesOutDec := len(decoded)

	// Use scratch buffer for resamplerIn if available
	var resamplerIn []int16
	if d.monoResamplerIn != nil && len(d.monoResamplerIn) >= nSamplesOutDec {
		resamplerIn = d.monoResamplerIn[:nSamplesOutDec]
	} else {
		resamplerIn = make([]int16, nSamplesOutDec)
	}
	resamplerIn[0] = d.stereo.sMid[1] // sMid[1] from previous frame
	copy(resamplerIn[1:], decoded[:nSamplesOutDec-1])

	inLen := len(resamplerIn)

	// Output buffer same size as decoded - use scratch buffer if available
	var output []int16
	if d.monoOutput != nil && len(d.monoOutput) >= len(decoded) {
		output = d.monoOutput[:len(decoded)]
	} else {
		output = make([]int16, len(decoded))
	}

	// Apply the libopus copy-resampler logic exactly:
	// silk_resampler() for USE_silk_resampler_copy case:
	//
	// nSamples = Fs_in_kHz - inputDelay;  // 8 - 4 = 4 for 8kHz
	// silk_memcpy(&delayBuf[inputDelay], in, nSamples * sizeof(opus_int16));
	// silk_memcpy(out, delayBuf, Fs_in_kHz * sizeof(opus_int16));
	// silk_memcpy(&out[Fs_out_kHz], &in[nSamples], (inLen - Fs_in_kHz) * sizeof(opus_int16));
	// silk_memcpy(delayBuf, &in[inLen - inputDelay], inputDelay * sizeof(opus_int16));

	nSamples := fsKHz - inputDelay

	// Step 1: Copy first nSamples of input to end of delay buffer
	copy(d.monoDelayBuf[inputDelay:], resamplerIn[:nSamples])

	// Step 2: Copy delay buffer to first fsKHz samples of output
	copy(output[:fsKHz], d.monoDelayBuf[:])

	// Step 3: Copy remaining input to rest of output
	// output[fsKHz:] = resamplerIn[nSamples:nSamples+(inLen-fsKHz)]
	if inLen > fsKHz {
		copy(output[fsKHz:], resamplerIn[nSamples:nSamples+(inLen-fsKHz)])
	}

	// Step 4: Save last inputDelay samples of input to start of delay buffer
	copy(d.monoDelayBuf[:inputDelay], resamplerIn[inLen-inputDelay:])

	// Update sMid with last 2 samples of decoded output (before delay processing)
	// libopus does: silk_memcpy(psDec->sStereo.sMid, &samplesOut1_tmp[0][nSamplesOutDec], 2)
	// where samplesOut1_tmp[0][2:] contains decoded samples
	// So sMid gets the last 2 samples of the decoded (not delayed) output
	if len(decoded) >= 2 {
		d.stereo.sMid[0] = decoded[len(decoded)-2]
		d.stereo.sMid[1] = decoded[len(decoded)-1]
	}

	return output
}

// SetRangeDecoder sets the range decoder for the current frame.
// This must be called before decoding each frame.
func (d *Decoder) SetRangeDecoder(rd *rangecoding.Decoder) {
	d.rangeDecoder = rd
}

// HaveDecoded returns whether at least one frame has been decoded.
// Used to determine if delta coding should be applied for gains.
func (d *Decoder) HaveDecoded() bool {
	return d.haveDecoded
}

// PreviousLogGain returns the previous frame's log gain value.
// Used for delta gain decoding.
func (d *Decoder) PreviousLogGain() int32 {
	return d.previousLogGain
}

// SetPreviousLogGain sets the log gain for delta coding.
func (d *Decoder) SetPreviousLogGain(gain int32) {
	d.previousLogGain = gain
}

// IsPreviousFrameVoiced returns whether the previous frame was voiced.
// Used for LTP filter application.
func (d *Decoder) IsPreviousFrameVoiced() bool {
	return d.isPreviousFrameVoiced
}

// SetPreviousFrameVoiced sets the voiced state for the previous frame.
func (d *Decoder) SetPreviousFrameVoiced(voiced bool) {
	d.isPreviousFrameVoiced = voiced
}

// MarkDecoded marks that a frame has been successfully decoded.
// This enables delta coding for subsequent frames.
func (d *Decoder) MarkDecoded() {
	d.haveDecoded = true
}

// LPCOrder returns the current LPC order (10 for NB/MB, 16 for WB).
func (d *Decoder) LPCOrder() int {
	return int(d.lpcOrder)
}

// SetLPCOrder sets the LPC order based on bandwidth.
func (d *Decoder) SetLPCOrder(order int) {
	d.lpcOrder = int32(order)
}

// PrevLPCValues returns the LPC filter state for continuity.
func (d *Decoder) PrevLPCValues() []float32 {
	return d.prevLPCValues
}

// PrevLSFQ15 returns the previous frame's LSF coefficients.
func (d *Decoder) PrevLSFQ15() []int16 {
	return d.prevLSFQ15
}

// SetPrevLSFQ15 copies LSF coefficients for interpolation with next frame.
func (d *Decoder) SetPrevLSFQ15(lsf []int16) {
	copy(d.prevLSFQ15, lsf)
}

// OutputHistory returns the output buffer for LTP lookback.
func (d *Decoder) OutputHistory() []float32 {
	return d.outputHistory
}

// HistoryIndex returns the current write position in the history buffer.
func (d *Decoder) HistoryIndex() int {
	return d.historyIndex
}

// SetHistoryIndex sets the write position in the history buffer.
func (d *Decoder) SetHistoryIndex(idx int) {
	d.historyIndex = idx
}

// PrevStereoWeights returns the previous stereo weights.
func (d *Decoder) PrevStereoWeights() [2]int16 {
	return d.prevStereoWeights
}

// SetPrevStereoWeights sets the stereo weights for the next frame.
func (d *Decoder) SetPrevStereoWeights(weights [2]int16) {
	d.prevStereoWeights = weights
}

// GetLastSignalType returns the signal type from the last decoded frame.
// Returns: 0=inactive, 1=unvoiced, 2=voiced
func (d *Decoder) GetLastSignalType() int {
	return int(d.state[0].indices.signalType)
}

// GetLagPrev returns the previous pitch lag tracked by SILK decode state.
func (d *Decoder) GetLagPrev() int {
	return int(d.state[0].lagPrev)
}

// LatestNativeMono returns the most recent native-rate (pre-resample) int16
// mono SILK output produced by decodeFrameRawInt16 (also used by
// DecodeWithDecoderInto), along with the native sample rate in kHz.
//
// The returned slice aliases internal scratch storage and must be consumed
// synchronously, before the next decode call. Returns (nil, 0) when no mono
// decode has run since the last Reset.
//
// This accessor exists for the optional OSCE BWE forward pass: libopus runs
// the BWE on the 16 kHz lowband produced by SILK, before silk_resampler
// upsamples to 48 kHz. The gopus equivalent path consumes this buffer
// directly so the BWE input matches libopus without performing a second
// decode pass.
func (d *Decoder) LatestNativeMono() ([]int16, int) {
	if d == nil || d.lastNativeMonoLen <= 0 || d.scratchOutInt16 == nil {
		return nil, 0
	}
	n := min(int(d.lastNativeMonoLen), len(d.scratchOutInt16))
	return d.scratchOutInt16[:n], int(d.lastNativeMonoFsKHz)
}

// LatestNativeStereo returns the most recent native-rate (pre-resample) int16
// left/right SILK output produced by `DecodeStereoFrameInt16Into` (used by the
// public `DecodeStereoWithDecoderInto`), along with the per-channel sample
// count and native sample rate in kHz.
//
// The returned slices alias internal scratch storage and must be consumed
// synchronously, before the next decode call. Returns (nil, nil, 0, 0, false)
// when no stereo decode has run since the last Reset.
//
// This accessor exists for the optional OSCE BWE forward pass: libopus runs
// the BWE on the 16 kHz lowband produced by SILK on each channel independently
// (one `silk_OSCE_BWE_struct` per `silk_channel_state`), before
// `silk_resampler` upsamples to 48 kHz. The gopus equivalent path consumes
// these buffers directly so the BWE input matches libopus without performing a
// second decode pass.
func (d *Decoder) LatestNativeStereo() (left, right []int16, samplesPerChannel, fsKHz int, ok bool) {
	if d == nil || d.lastNativeStereoLen <= 0 {
		return nil, nil, 0, 0, false
	}
	n := int(d.lastNativeStereoLen)
	if n > len(d.stereoLeftNative) || n > len(d.stereoRightNative) {
		return nil, nil, 0, 0, false
	}
	return d.stereoLeftNative[:n], d.stereoRightNative[:n], n, int(d.lastNativeStereoFsKHz), true
}

// LatestNativeMid returns the most recent native-rate internal SILK channel 0
// samples before stereo MS->LR conversion.
//
// This is distinct from LatestNativeStereo, which exposes post-MS->LR
// left/right lowband output. libopus feeds decoder-side DeepPLC/DRED from SILK
// channel 0 only, so callers matching that path should prefer this accessor
// when a true stereo packet was decoded.
func (d *Decoder) LatestNativeMid() ([]int16, int) {
	if d == nil || d.lastNativeMidLen <= 0 || d.stereoMidNative == nil {
		return nil, 0
	}
	n := min(int(d.lastNativeMidLen), len(d.stereoMidNative))
	return d.stereoMidNative[:n], int(d.lastNativeMidFsKHz)
}

// LatestDecoderControl is the public view of the most recent
// `silk_decoder_control` decoded for a channel. It mirrors the libopus
// `silk_decoder_control` fields the OSCE LACE / NoLACE feature extractor
// reads (`osce_features.c::osce_calculate_features`): per-subframe LPC
// prediction coefficients (Q12), LTP filter coefficients (Q14),
// subframe gains (Q16), pitch lags, and the SILK signal type for the
// frame. Field names mirror the libopus C struct so future cross-
// referencing stays mechanical.
type LatestDecoderControl struct {
	PredCoefQ12 [2][maxLPCOrder]int16
	LTPCoefQ14  [ltpOrder * maxNbSubfr]int16
	GainsQ16    [maxNbSubfr]int32
	PitchL      [maxNbSubfr]int32
	LPCOrder    int32
	NbSubfr     int32
	SignalType  int32
	FsKHz       int32
	NumBits     int32
}

// LatestDecoderControl returns the per-frame SILK decoder control state for
// the most recent good-frame decode on `channel` (0 = mono / mid / left,
// 1 = side / right). Returns (zero, false) when no decoded ctrl has been
// cached (e.g. before the first decode, after a Reset, or for PLC frames
// which bypass `finalizeDecodedChannelFrame`).
//
// The accessor exists so optional decoder-side post-processing -- in
// particular the OSCE LACE / NoLACE postfilter feature extractor -- can
// read libopus' per-frame `silk_decoder_control` fields without performing
// a second decode pass.
func (d *Decoder) LatestDecoderControl(channel int) (LatestDecoderControl, bool) {
	if d == nil || channel < 0 || channel >= len(d.lastFrameCtrl) {
		return LatestDecoderControl{}, false
	}
	if !d.lastFrameCtrlValid[channel] {
		return LatestDecoderControl{}, false
	}
	st := &d.state[channel]
	src := d.lastFrameCtrl[channel]
	out := LatestDecoderControl{
		PredCoefQ12: src.PredCoefQ12,
		LTPCoefQ14:  src.LTPCoefQ14,
		GainsQ16:    src.GainsQ16,
		PitchL:      src.pitchL,
		SignalType:  d.lastFrameCtrlSignal[channel],
		LPCOrder:    st.lpcOrder,
		NbSubfr:     st.nbSubfr,
		FsKHz:       st.fsKHz,
		NumBits:     src.NumBits,
	}
	return out, true
}

// RawMonoFrameHook fires on raw mono/mid 10 ms chunks before CNG/glue mutates
// the decoded frame buffer. The slice aliases decoder scratch memory and must
// be consumed synchronously.
type RawMonoFrameHook func(samples []int16)

// DeepPLCLossMonoHook fires during mono 16 kHz PLC before SILK updates its
// retained loss/output history. The hook fills concealed with one lost lowband
// frame in normalized float32 units and may optionally return a lagPrev value
// to retain for the next good packet.
type DeepPLCLossMonoHook func(concealed []float32) (ok bool, lagPrev int)

type resamplerPair struct {
	left  *LibopusResampler
	right *LibopusResampler
}

// DeepPLCLowbandSnapshot is an opaque capture of the mono low-band decoder
// state (channel 0, including the wideband resampler and SILK PLC state) taken
// before deep-PLC concealment so the decoder can be rolled back and advanced
// over concealed samples.
type DeepPLCLowbandSnapshot struct {
	stereo        stereoDecState
	state         decoderState
	silkPLCState  plc.SILKPLCState
	hasPLCState   bool
	resampler     libopusResamplerSnapshot
	outputHistory []float32
	historyIndex  int
	prevLPCValues []float32
}

// GetResampler returns the libopus-compatible resampler for the given bandwidth.
// This returns the left/mono resampler.
func (d *Decoder) GetResampler(bandwidth Bandwidth) *LibopusResampler {
	return d.GetResamplerForChannel(bandwidth, 0)
}

// GetResamplerRightChannel returns the right channel resampler for the given bandwidth.
func (d *Decoder) GetResamplerRightChannel(bandwidth Bandwidth) *LibopusResampler {
	return d.GetResamplerForChannel(bandwidth, 1)
}

// GetResamplerForChannel returns the resampler for the specified channel and bandwidth.
func (d *Decoder) GetResamplerForChannel(bandwidth Bandwidth, channel int) *LibopusResampler {
	if d.resamplers == nil {
		d.resamplers = make(map[Bandwidth]*resamplerPair)
	}

	pair, ok := d.resamplers[bandwidth]
	if !ok {
		pair = &resamplerPair{}
		d.resamplers[bandwidth] = pair
	}

	config := GetBandwidthConfig(bandwidth)
	if channel == 1 {
		if pair.right == nil {
			pair.right = NewLibopusResampler(config.SampleRate, d.outputSampleRate())
		}
		return pair.right
	}

	if pair.left == nil {
		pair.left = NewLibopusResampler(config.SampleRate, d.outputSampleRate())
	}
	return pair.left
}

// HandleBandwidthChange checks if bandwidth has changed.
// This must be called before BuildMonoResamplerInput when bandwidth may have changed.
// Returns true if bandwidth changed.
//
// Note: libopus does NOT reset sMid on bandwidth change. Only the resampler internal
// state is zeroed. sMid values from the previous bandwidth are preserved for continuity.
func (d *Decoder) HandleBandwidthChange(bandwidth Bandwidth) bool {
	if !d.hasPrevBandwidth {
		d.prevBandwidth = bandwidth
		d.hasPrevBandwidth = true
		return false
	}
	if d.prevBandwidth != bandwidth {
		// Bandwidth changed - do NOT reset sMid to match libopus behavior.
		// The resampler internal state is reset in handleBandwidthChange().
		d.prevBandwidth = bandwidth
		return true
	}
	return false
}

func (d *Decoder) updateMonoHistoryFromFloat32(samples []float32) {
	if len(samples) > 1 {
		d.stereo.sMid[0] = float32ToInt16(samples[len(samples)-2])
		d.stereo.sMid[1] = float32ToInt16(samples[len(samples)-1])
		return
	}
	d.stereo.sMid[0] = d.stereo.sMid[1]
	d.stereo.sMid[1] = float32ToInt16(samples[0])
}

func (d *Decoder) updateMonoHistoryFromInt16(samples []int16) {
	if len(samples) > 1 {
		d.stereo.sMid[0] = samples[len(samples)-2]
		d.stereo.sMid[1] = samples[len(samples)-1]
		return
	}
	d.stereo.sMid[0] = d.stereo.sMid[1]
	d.stereo.sMid[1] = samples[0]
}

// SnapshotDeepPLCLowbandMono captures the current mono low-band decoder state
// for later restore, returning nil if the decoder or its wideband resampler is
// unavailable.
func (d *Decoder) SnapshotDeepPLCLowbandMono() *DeepPLCLowbandSnapshot {
	if d == nil {
		return nil
	}
	resampler := d.GetResampler(BandwidthWideband)
	if resampler == nil {
		return nil
	}
	snap := &DeepPLCLowbandSnapshot{
		stereo:        d.stereo,
		state:         d.state[0],
		resampler:     resampler.snapshot(),
		outputHistory: append([]float32(nil), d.outputHistory...),
		historyIndex:  d.historyIndex,
		prevLPCValues: append([]float32(nil), d.prevLPCValues...),
	}
	if d.silkPLCState[0] != nil {
		snap.silkPLCState = *d.silkPLCState[0]
		snap.hasPLCState = true
	}
	return snap
}

// RestoreDeepPLCLowbandMono restores the mono low-band decoder state previously
// captured by SnapshotDeepPLCLowbandMono. It is a no-op for a nil decoder or
// snapshot.
func (d *Decoder) RestoreDeepPLCLowbandMono(s *DeepPLCLowbandSnapshot) {
	if d == nil || s == nil {
		return
	}
	d.stereo = s.stereo
	if resampler := d.GetResampler(BandwidthWideband); resampler != nil {
		resampler.restore(s.resampler)
	}
	d.state[0] = s.state
	if s.hasPLCState {
		if d.silkPLCState[0] == nil {
			d.silkPLCState[0] = &plc.SILKPLCState{}
		}
		*d.silkPLCState[0] = s.silkPLCState
	} else {
		d.silkPLCState[0] = nil
	}
	if s.outputHistory != nil {
		if cap(d.outputHistory) < len(s.outputHistory) {
			d.outputHistory = make([]float32, len(s.outputHistory))
		} else {
			d.outputHistory = d.outputHistory[:len(s.outputHistory)]
		}
		copy(d.outputHistory, s.outputHistory)
	}
	d.historyIndex = s.historyIndex
	if s.prevLPCValues != nil {
		if cap(d.prevLPCValues) < len(s.prevLPCValues) {
			d.prevLPCValues = make([]float32, len(s.prevLPCValues))
		} else {
			d.prevLPCValues = d.prevLPCValues[:len(s.prevLPCValues)]
		}
		copy(d.prevLPCValues, s.prevLPCValues)
	}
}

// AdvanceDeepPLCLowbandMono feeds concealed low-band samples through the mono
// wideband resampler to advance the decoder's resampler state, returning false
// if the decoder, input, or resampler is unavailable.
func (d *Decoder) AdvanceDeepPLCLowbandMono(concealed []float32) bool {
	if d == nil || len(concealed) == 0 {
		return false
	}
	resampler := d.GetResampler(BandwidthWideband)
	if resampler == nil {
		return false
	}
	var native []int16
	if d.scratchOutInt16 != nil && len(d.scratchOutInt16) >= len(concealed) {
		native = d.scratchOutInt16[:len(concealed)]
	} else {
		native = make([]int16, len(concealed))
	}
	for i, sample := range concealed {
		native[i] = float32ToInt16(sample)
	}
	in := d.BuildMonoResamplerInputInt16(native)
	needed := len(concealed) * 3
	if needed <= 0 {
		return false
	}
	if cap(d.upsampleScratch) < needed {
		d.upsampleScratch = make([]float32, needed)
	}
	resampler.ProcessInt16Into(in, d.upsampleScratch[:needed])
	return true
}

// BuildMonoResamplerInput prepares the mono resampler input using libopus-style sMid buffering.
// It updates the internal sMid state based on the current samples.
func (d *Decoder) BuildMonoResamplerInput(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}

	// Use pre-allocated scratch buffer if available
	var resamplerInput []float32
	if d.buildMonoInputScratch != nil && len(d.buildMonoInputScratch) >= len(samples) {
		resamplerInput = d.buildMonoInputScratch[:len(samples)]
	} else {
		resamplerInput = make([]float32, len(samples))
	}
	resamplerInput[0] = float32(d.stereo.sMid[1]) / 32768.0

	if len(samples) > 1 {
		copy(resamplerInput[1:], samples[:len(samples)-1])
	}
	d.updateMonoHistoryFromFloat32(samples)

	return resamplerInput
}

// BuildMonoResamplerInputInt16 prepares mono resampler input with libopus-style sMid buffering.
// This int16 variant is used by decoder hot paths to avoid float32->int16 reconversion.
func (d *Decoder) BuildMonoResamplerInputInt16(samples []int16) []int16 {
	if len(samples) == 0 {
		return nil
	}

	var resamplerInput []int16
	if d.monoResamplerIn != nil && len(d.monoResamplerIn) >= len(samples) {
		resamplerInput = d.monoResamplerIn[:len(samples)]
	} else {
		resamplerInput = make([]int16, len(samples))
	}
	resamplerInput[0] = d.stereo.sMid[1]

	if len(samples) > 1 {
		copy(resamplerInput[1:], samples[:len(samples)-1])
	}
	d.updateMonoHistoryFromInt16(samples)

	return resamplerInput
}

// GetStereoInt16Scratch returns decoder-owned native-rate stereo scratch
// buffers. The returned slices are invalidated by the next decode call.
func (d *Decoder) GetStereoInt16Scratch(samples int) (left, right []int16, ok bool) {
	if samples <= 0 {
		return nil, nil, false
	}
	if cap(d.stereoLeftNative) < samples || cap(d.stereoRightNative) < samples {
		return nil, nil, false
	}
	return d.stereoLeftNative[:samples], d.stereoRightNative[:samples], true
}

func (d *Decoder) stereoFrameScratch(frameLength int) (mid, side []int16, ok bool) {
	needed := frameLength + 2
	if frameLength <= 0 || cap(d.stereoMidFrame) < needed || cap(d.stereoSideFrame) < needed {
		return nil, nil, false
	}
	return d.stereoMidFrame[:needed], d.stereoSideFrame[:needed], true
}

// ResetSideChannel resets the mono->stereo bitstream transition state.
// This mirrors libopus silk/dec_API.c for nChannelsInternal 1 -> 2:
// re-init the side decoder, clear the stereo side/predictor history, and
// reset the right-channel resampler before copying left-channel history over.
func (d *Decoder) ResetSideChannel() {
	resetDecoderState(&d.state[1])
	d.setupScratchBuffers()
	d.stereo.predPrevQ13 = [2]int16{}
	d.stereo.sSide = [2]int16{}
	if d.resamplers == nil {
		return
	}
	for _, pair := range d.resamplers {
		if pair == nil || pair.right == nil {
			continue
		}
		pair.right.Reset()
	}
}

// ShouldUseStereoToMonoHistory mirrors libopus silk/dec_API.c stereo_to_mono.
// The right-channel resampler history is only valid when the previous internal
// stream was stereo and the internal sample rate did not change.
func (d *Decoder) ShouldUseStereoToMonoHistory(bandwidth Bandwidth, prevPacketStereo bool) bool {
	if !prevPacketStereo {
		return false
	}
	config := GetBandwidthConfig(bandwidth)
	return d.state[0].fsKHz != 0 && config.SampleRate == int(d.state[0].fsKHz)*1000
}

// handleBandwidthChange detects sample rate changes and resets the appropriate resampler.
// In libopus, when the internal sample rate changes (NB 8kHz <-> MB 12kHz <-> WB 16kHz),
// the resampler for the NEW bandwidth needs to be reset to avoid using stale state.
//
// IMPORTANT: libopus does NOT reset sMid on bandwidth change - it keeps the previous
// sample values. Only the resampler internal state (sIIR, sFIR, delayBuf) is zeroed via
// silk_resampler_init(). The sMid values from the previous bandwidth are preserved and
// used as the first input sample to the new resampler, which causes a small transient
// but maintains signal continuity at bandwidth transitions.
//
// NOTE: This is also called by the Hybrid decoder via NotifyBandwidthChange to ensure
// proper resampler state management when mixing SILK-only and Hybrid packets.
func (d *Decoder) handleBandwidthChange(bandwidth Bandwidth) {
	if d.hasPrevBandwidth && d.prevBandwidth != bandwidth {
		// Sample rate changed - reset the resampler for the NEW bandwidth
		// but keep sMid values to match libopus behavior.
		if pair, ok := d.resamplers[bandwidth]; ok && pair != nil {
			if pair.left != nil {
				pair.left.Reset()
			}
			if pair.right != nil {
				pair.right.Reset()
			}
		}
	}
	d.prevBandwidth = bandwidth
	d.hasPrevBandwidth = true
}

// NotifyBandwidthChange updates bandwidth tracking and resets the resampler if needed.
// This should be called by the Hybrid decoder before using SILK to ensure proper
// resampler state when transitioning between SILK-only and Hybrid modes.
//
// When Hybrid mode uses SILK at BandwidthWideband, calling this method ensures that:
// 1. The prevBandwidth is updated to WB
// 2. If transitioning TO WB, the WB resampler is reset
// 3. When later transitioning back to SILK NB/MB, the correct resampler will be reset
func (d *Decoder) NotifyBandwidthChange(bandwidth Bandwidth) {
	d.handleBandwidthChange(bandwidth)
}

// GetResamplerScratch returns a pre-allocated buffer for resampler output (left channel).
// This is used by the Hybrid decoder for zero-allocation SILK upsampling.
func (d *Decoder) GetResamplerScratch(frameSize int) []float32 {
	// Max output is frameSize samples (already at 48kHz after resampling)
	if cap(d.resamplerScratchResult) < frameSize {
		d.resamplerScratchResult = make([]float32, frameSize)
	} else {
		d.resamplerScratchResult = d.resamplerScratchResult[:frameSize]
	}
	return d.resamplerScratchResult
}

// GetResamplerScratchR returns a pre-allocated buffer for right channel resampler output.
// This is used by the Hybrid decoder for zero-allocation stereo SILK upsampling.
func (d *Decoder) GetResamplerScratchR(frameSize int) []float32 {
	// Max output is frameSize samples (already at 48kHz after resampling)
	if cap(d.upsampleScratch) < frameSize {
		d.upsampleScratch = make([]float32, frameSize)
	} else {
		d.upsampleScratch = d.upsampleScratch[:frameSize]
	}
	return d.upsampleScratch
}

// FinalRange returns the final range coder state after decoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after decoding a frame to get a meaningful value.
func (d *Decoder) FinalRange() uint32 {
	if d.rangeDecoder != nil {
		return d.rangeDecoder.Range()
	}
	return 0
}

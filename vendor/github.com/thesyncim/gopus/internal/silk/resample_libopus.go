package silk

// LibopusResampler implements the exact SILK resampler from libopus.
// It follows libopus' copy, direct 2x, IIR/FIR upsampling, and downsampling paths.
type LibopusResampler struct {
	// IIR state for 2x upsampler (6 elements for 3rd order allpass)
	sIIR [6]int32

	// FIR delay buffer (8 samples for the 8-tap symmetric FIR)
	sFIR [8]int16

	// Configuration
	invRatioQ16 int32 // Input/output ratio in Q16
	batchSize   int32 // Number of samples per batch
	inputDelay  int32 // Delay compensation
	fsInKHz     int32
	fsOutKHz    int32

	// Delay buffer for continuity (size = fsInKHz)
	delayBuf []int16

	// Pre-allocated scratch buffers for zero-allocation resampling
	scratchBuf    []int16   // Size: 2*batchSize + resamplerOrderFIR12
	scratchIn     []int16   // Size: max input samples
	scratchOut    []int16   // Size: max output samples
	scratchResult []float32 // Size: max output samples

	copyMode  bool
	up2HQMode bool
	down      *DownsamplingResampler // down_FIR stage, set when fsOut < fsIn
	idleDown  *DownsamplingResampler // down_FIR stage kept for reuse by init
}

type libopusResamplerSnapshot struct {
	sIIR      [6]int32
	sFIR      [8]int16
	delayBuf  []int16
	down      DownsamplingResamplerState
	hasDown   bool
	copyMode  bool
	up2HQMode bool
}

func (r *LibopusResampler) snapshot() libopusResamplerSnapshot {
	if r == nil {
		return libopusResamplerSnapshot{}
	}
	s := libopusResamplerSnapshot{
		sIIR:      r.sIIR,
		sFIR:      r.sFIR,
		copyMode:  r.copyMode,
		up2HQMode: r.up2HQMode,
	}
	if len(r.delayBuf) != 0 {
		s.delayBuf = append([]int16(nil), r.delayBuf...)
	}
	if r.down != nil {
		s.down = r.down.State()
		s.hasDown = true
	}
	return s
}

func (r *LibopusResampler) restore(s libopusResamplerSnapshot) {
	if r == nil {
		return
	}
	r.sIIR = s.sIIR
	r.sFIR = s.sFIR
	r.copyMode = s.copyMode
	r.up2HQMode = s.up2HQMode
	if len(s.delayBuf) == 0 {
		r.delayBuf = nil
	} else {
		if cap(r.delayBuf) < len(s.delayBuf) {
			r.delayBuf = make([]int16, len(s.delayBuf))
		} else {
			r.delayBuf = r.delayBuf[:len(s.delayBuf)]
		}
		copy(r.delayBuf, s.delayBuf)
	}
	if s.hasDown && r.down != nil {
		r.down.SetState(s.down)
	}
}

// resampleIIRFIRSliceWithScratch is like resampleIIRFIRSlice but uses a pre-allocated scratch buffer.
func (r *LibopusResampler) resampleIIRFIRSliceWithScratch(out []int16, in []int16, scratch []int16) {
	inLen := int32(len(in))
	outIdx := 0

	// Use pre-allocated buffer for 2x upsampled data + FIR history
	bufSize := int(2*r.batchSize + resamplerOrderFIR12)
	var buf []int16
	if scratch != nil && len(scratch) >= bufSize {
		buf = scratch[:bufSize]
	} else if r.scratchBuf != nil && len(r.scratchBuf) >= bufSize {
		buf = r.scratchBuf[:bufSize]
	} else {
		buf = make([]int16, bufSize)
	}

	// Copy FIR state to start of buffer
	copy(buf, r.sFIR[:])

	inOffset := int32(0)
	var lastNSamplesIn int32
	for {
		nSamplesIn := min32(inLen, r.batchSize)
		lastNSamplesIn = nSamplesIn

		// 2x upsample using allpass filters
		r.up2HQ(buf[resamplerOrderFIR12:], in[inOffset:inOffset+nSamplesIn])

		// FIR interpolation
		maxIndexQ16 := nSamplesIn << 17 // nSamplesIn * 2 * 65536
		outIdx = r.firInterpol(out, outIdx, buf, maxIndexQ16)

		inOffset += nSamplesIn
		inLen -= nSamplesIn

		if inLen > 0 {
			// Copy last part of buffer to beginning for next iteration
			copy(buf, buf[nSamplesIn*2:nSamplesIn*2+resamplerOrderFIR12])
		} else {
			break
		}
	}

	// Save FIR state for next call
	copy(r.sFIR[:], buf[lastNSamplesIn*2:lastNSamplesIn*2+resamplerOrderFIR12])
}

var silkResamplerFracFIR12Flat = [48]int16{
	189, -600, 617, 30567,
	117, -159, -1070, 29704,
	52, 221, -2392, 28276,
	-4, 529, -3350, 26341,
	-48, 758, -3956, 23973,
	-80, 905, -4235, 21254,
	-99, 972, -4222, 18278,
	-107, 967, -3957, 15143,
	-103, 896, -3487, 11950,
	-91, 773, -2865, 8798,
	-71, 611, -2143, 5784,
	-46, 425, -1375, 2996,
}

const (
	fir0c0 int32 = 189
	fir0c1 int32 = -600
	fir0c2 int32 = 617
	fir0c3 int32 = 30567
	fir0c4 int32 = 2996
	fir0c5 int32 = -1375
	fir0c6 int32 = 425
	fir0c7 int32 = -46

	fir4c0 int32 = -48
	fir4c1 int32 = 758
	fir4c2 int32 = -3956
	fir4c3 int32 = 23973
	fir4c4 int32 = 15143
	fir4c5 int32 = -3957
	fir4c6 int32 = 967
	fir4c7 int32 = -107

	fir6c0 int32 = -99
	fir6c1 int32 = 972
	fir6c2 int32 = -4222
	fir6c3 int32 = 18278
	fir6c4 int32 = 21254
	fir6c5 int32 = -4235
	fir6c6 int32 = 905
	fir6c7 int32 = -80

	fir8c0 int32 = -103
	fir8c1 int32 = 896
	fir8c2 int32 = -3487
	fir8c3 int32 = 11950
	fir8c4 int32 = 26341
	fir8c5 int32 = -3350
	fir8c6 int32 = 529
	fir8c7 int32 = -4
)

// Delay matrix for decoder (from resampler.c)
// in \ out  8  12  16  24  48  96
var delayMatrixDec = [3][6]int8{
	/*  8 */ {4, 0, 2, 0, 0, 0},
	/* 12 */ {0, 9, 4, 7, 4, 4},
	/* 16 */ {0, 3, 12, 7, 7, 7},
}

// rateID converts sample rate to index: 8000->0, 12000->1, 16000->2,
// 24000->3, 48000->4, and 96000->5.
func rateID(rate int) int {
	switch rate {
	case 8000:
		return 0
	case 12000:
		return 1
	case 16000:
		return 2
	case 24000:
		return 3
	case 48000:
		return 4
	case 96000:
		return 5
	default:
		return 0
	}
}

const resamplerOrderFIR12 = 8
const resamplerMaxBatchSizeMs = 10
const resamplerMaxFrameMs = 60

// NewLibopusResampler creates a new resampler matching libopus behavior.
//
// This is the decoder-side constructor (forEnc=0 in silk_resampler_init): the
// input delay comes from delay_matrix_dec and downsampling uses the decoder
// down_FIR. For the encoder input resampler (API_fs_Hz -> fs_kHz) use
// NewLibopusResamplerEnc, which selects delay_matrix_enc and the encoder
// down_FIR.
func NewLibopusResampler(fsIn, fsOut int) *LibopusResampler {
	return newLibopusResampler(fsIn, fsOut, false)
}

// NewLibopusResamplerEnc creates the encoder-side SILK input resampler, matching
// silk_resampler_init(..., forEnc=1): API sample rate fsIn (8/12/16/24/48/96 kHz)
// to internal rate fsOut (8/12/16 kHz), using delay_matrix_enc and the encoder
// down_FIR. It supports the copy (Fs_in==Fs_out), direct 2x (Fs_out==2*Fs_in),
// IIR/FIR (other upsample) and down_FIR (downsample) paths.
func NewLibopusResamplerEnc(fsIn, fsOut int) *LibopusResampler {
	return newLibopusResampler(fsIn, fsOut, true)
}

func newLibopusResampler(fsIn, fsOut int, forEnc bool) *LibopusResampler {
	r := &LibopusResampler{}
	r.init(fsIn, fsOut, forEnc)
	return r
}

// init is silk_resampler_init (silk/resampler.c): it clears the state and
// configures the fsIn -> fsOut conversion, forEnc selecting the encoder
// (delay_matrix_enc) or the decoder (delay_matrix_dec) delay compensation. The
// state and scratch buffers are reused when they are large enough.
func (r *LibopusResampler) init(fsIn, fsOut int, forEnc bool) {
	r.sIIR = [6]int32{}
	r.sFIR = [8]int16{}
	r.fsInKHz = int32(fsIn / 1000)
	r.fsOutKHz = int32(fsOut / 1000)
	r.inputDelay = 0
	r.invRatioQ16 = 0
	r.batchSize = 0
	r.copyMode = false
	r.up2HQMode = false

	// Delay compensation from libopus (delay_matrix_enc for the encoder input
	// resampler, delay_matrix_dec for the decoder output resampler).
	if forEnc {
		inIdx := rateIDEnc(fsIn)
		outIdx := rateIDEnc(fsOut)
		if inIdx >= 0 && inIdx < 6 && outIdx >= 0 && outIdx < 3 {
			r.inputDelay = int32(delayMatrixEnc[inIdx][outIdx])
		}
	} else {
		inIdx := rateID(fsIn)
		outIdx := rateID(fsOut)
		if inIdx < 3 && outIdx < 6 {
			r.inputDelay = int32(delayMatrixDec[inIdx][outIdx])
		}
	}

	if fsOut < fsIn {
		if r.down == nil {
			r.down, r.idleDown = r.idleDown, nil
			if r.down == nil {
				r.down = &DownsamplingResampler{}
			}
		}
		r.down.init(fsIn, fsOut, forEnc)
		r.delayBuf = r.delayBuf[:0]
		return
	}
	if r.down != nil {
		r.idleDown, r.down = r.down, nil
	}
	clear(ensureInt16Slice(&r.delayBuf, int(r.fsInKHz)))
	maxInputSamples := int(r.fsInKHz * resamplerMaxFrameMs)
	maxOutputSamples := int(r.fsOutKHz * resamplerMaxFrameMs)
	ensureInt16Slice(&r.scratchIn, maxInputSamples)
	ensureInt16Slice(&r.scratchOut, maxOutputSamples)
	ensureFloat32Slice(&r.scratchResult, maxOutputSamples)
	if fsOut == fsIn {
		r.copyMode = true
		return
	}
	if fsOut == fsIn*2 {
		r.up2HQMode = true
		return
	}

	// Batch size
	r.batchSize = r.fsInKHz * resamplerMaxBatchSizeMs

	// Calculate invRatio_Q16 for upsampling
	// For IIR_FIR: up2x = 1, so we first 2x upsample
	// invRatio_Q16 = ((Fs_in << (14 + up2x)) / Fs_out) << 2
	up2x := 1
	invRatio := int32((fsIn << (14 + up2x)) / fsOut)
	r.invRatioQ16 = invRatio << 2

	// Make sure the ratio is rounded up
	for smulww(r.invRatioQ16, int32(fsOut)) < int32(fsIn<<up2x) {
		r.invRatioQ16++
	}

	ensureInt16Slice(&r.scratchBuf, int(2*r.batchSize+resamplerOrderFIR12))
}

// ResamplerState holds the internal state of the resampler. For downsampling
// configurations the work happens in the delegated down_FIR resampler, so the
// snapshot carries that state too.
type ResamplerState struct {
	sIIR     [6]int32
	sFIR     [8]int16
	delayBuf []int16
	down     DownsamplingResamplerState
	hasDown  bool
}

// State returns a snapshot of the current resampler state.
func (r *LibopusResampler) State() ResamplerState {
	if r.down != nil {
		return ResamplerState{down: r.down.State(), hasDown: true}
	}
	s := ResamplerState{
		sIIR: r.sIIR,
		sFIR: r.sFIR,
	}
	if len(r.delayBuf) > 0 {
		s.delayBuf = make([]int16, len(r.delayBuf))
		copy(s.delayBuf, r.delayBuf)
	}
	return s
}

// SetState restores the resampler state from a snapshot.
func (r *LibopusResampler) SetState(s ResamplerState) {
	if r.down != nil {
		if s.hasDown {
			r.down.SetState(s.down)
		} else {
			r.down.SetState(DownsamplingResamplerState{})
		}
		return
	}
	r.sIIR = s.sIIR
	r.sFIR = s.sFIR
	if len(s.delayBuf) == 0 {
		for i := range r.delayBuf {
			r.delayBuf[i] = 0
		}
		return
	}
	if len(r.delayBuf) >= len(s.delayBuf) {
		copy(r.delayBuf, s.delayBuf)
	}
}

// Reset clears the resampler state.
func (r *LibopusResampler) Reset() {
	if r.down != nil {
		r.down.SetState(DownsamplingResamplerState{})
	}
	for i := range r.sIIR {
		r.sIIR[i] = 0
	}
	for i := range r.sFIR {
		r.sFIR[i] = 0
	}
	for i := range r.delayBuf {
		r.delayBuf[i] = 0
	}
}

// CopyFrom copies state from another resampler.
// This is used to sync stereo resampler state when switching from mono.
func (r *LibopusResampler) CopyFrom(src *LibopusResampler) {
	if r == nil || src == nil {
		return
	}

	r.sIIR = src.sIIR
	r.sFIR = src.sFIR
	r.invRatioQ16 = src.invRatioQ16
	r.batchSize = src.batchSize
	r.inputDelay = src.inputDelay
	r.fsInKHz = src.fsInKHz
	r.fsOutKHz = src.fsOutKHz
	r.copyMode = src.copyMode
	r.up2HQMode = src.up2HQMode

	if src.delayBuf == nil {
		r.delayBuf = nil
	} else {
		if len(r.delayBuf) != len(src.delayBuf) {
			r.delayBuf = make([]int16, len(src.delayBuf))
		}
		copy(r.delayBuf, src.delayBuf)
	}
	if src.down != nil {
		if r.down == nil {
			r.down = newDecoderDownsamplingResampler(int(src.fsInKHz*1000), int(src.fsOutKHz*1000))
		}
		r.down.CopyFrom(src.down)
	} else {
		r.down = nil
	}
}

// prepareInputFromFloat32 converts input to int16 and pads to >=1ms if needed.
func (r *LibopusResampler) prepareInputFromFloat32(samples []float32) ([]int16, int32) {
	inLen := int32(len(samples))
	neededLen := len(samples)
	if inLen < r.fsInKHz {
		neededLen = int(r.fsInKHz)
	}

	var in []int16
	if r.scratchIn != nil && len(r.scratchIn) >= neededLen {
		in = r.scratchIn[:neededLen]
	} else {
		in = make([]int16, neededLen)
	}

	for i, s := range samples {
		in[i] = float32ToInt16(s)
	}
	if neededLen > len(samples) {
		clear(in[len(samples):])
		inLen = r.fsInKHz
	}
	return in, inLen
}

// prepareInputFromInt16 pads int16 input to >=1ms if needed.
func (r *LibopusResampler) prepareInputFromInt16(samples []int16) ([]int16, int32) {
	inLen := int32(len(samples))
	if inLen >= r.fsInKHz {
		return samples, inLen
	}

	neededLen := int(r.fsInKHz)
	var in []int16
	if r.scratchIn != nil && len(r.scratchIn) >= neededLen {
		in = r.scratchIn[:neededLen]
		clear(in)
	} else {
		in = make([]int16, neededLen)
	}
	copy(in, samples)
	return in, r.fsInKHz
}

// processInt16Core runs the libopus-matching resampler core for int16 input.
func (r *LibopusResampler) processInt16Core(in []int16, inLen int32) []int16 {
	outLen := int(inLen) * int(r.fsOutKHz) / int(r.fsInKHz)
	var outInt16 []int16
	if r.scratchOut != nil && len(r.scratchOut) >= outLen {
		outInt16 = r.scratchOut[:outLen]
	} else {
		outInt16 = make([]int16, outLen)
	}

	if r.copyMode {
		nSamples := r.fsInKHz - r.inputDelay
		copy(r.delayBuf[int(r.inputDelay):], in[:int(nSamples)])
		copy(outInt16[:int(r.fsOutKHz)], r.delayBuf[:int(r.fsInKHz)])
		if inLen > r.fsInKHz {
			copy(outInt16[int(r.fsOutKHz):], in[int(nSamples):int(nSamples+inLen-r.fsInKHz)])
		}
		if r.inputDelay > 0 {
			copy(r.delayBuf[:int(r.inputDelay)], in[int(inLen-r.inputDelay):int(inLen)])
		}
		return outInt16
	}

	if r.up2HQMode {
		nSamples := r.fsInKHz - r.inputDelay
		copy(r.delayBuf[int(r.inputDelay):], in[:int(nSamples)])
		r.up2HQ(outInt16[:int(r.fsOutKHz)], r.delayBuf[:int(r.fsInKHz)])

		if inLen > r.fsInKHz {
			end := min(max(inLen-r.inputDelay, nSamples), inLen)
			r.up2HQ(outInt16[int(r.fsOutKHz):], in[int(nSamples):int(end)])
		}

		if r.inputDelay > 0 {
			copy(r.delayBuf[:int(r.inputDelay)], in[int(inLen-r.inputDelay):int(inLen)])
		}
		return outInt16
	}

	// Match libopus silk_resampler() flow:
	// 1) Prime delay buffer with current input
	// 2) Process delay buffer (1 ms)
	// 3) Process remaining input
	// 4) Preserve tail in delay buffer
	nSamples := r.fsInKHz - r.inputDelay
	copy(r.delayBuf[int(r.inputDelay):], in[:int(nSamples)])
	r.resampleIIRFIRSliceWithScratch(outInt16[:int(r.fsOutKHz)], r.delayBuf[:int(r.fsInKHz)], r.scratchBuf)

	if inLen > r.fsInKHz {
		end := min(max(inLen-r.inputDelay, nSamples), inLen)
		r.resampleIIRFIRSliceWithScratch(outInt16[int(r.fsOutKHz):], in[int(nSamples):int(end)], r.scratchBuf)
	}

	if r.inputDelay > 0 {
		copy(r.delayBuf[:int(r.inputDelay)], in[int(inLen-r.inputDelay):int(inLen)])
	}

	return outInt16
}

// Resample is silk_resampler (silk/resampler.c): it resamples in (at least
// 1 ms of input) into out, which holds len(in)*fsOut/fsIn samples.
func (r *LibopusResampler) Resample(out, in []int16) {
	if r.down != nil {
		r.down.processWithDelay(out, in)
		return
	}
	copy(out, r.processInt16Core(in, int32(len(in))))
}

func writeInt16AsFloat32(dst []float32, src []int16) int {
	written := min(len(src), len(dst))
	if written > 0 {
		writeInt16AsFloat32Core(dst[:written:written], src[:written:written], written)
	}
	return written
}

// Process resamples float32 samples from input rate to output rate.
// This implements the exact libopus silk_resampler() flow with delay buffer.
func (r *LibopusResampler) Process(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}
	if r.down != nil {
		return r.down.Process(samples)
	}

	in, inLen := r.prepareInputFromFloat32(samples)
	outInt16 := r.processInt16Core(in, inLen)

	var result []float32
	if r.scratchResult != nil && len(r.scratchResult) >= len(outInt16) {
		result = r.scratchResult[:len(outInt16)]
	} else {
		result = make([]float32, len(outInt16))
	}
	written := writeInt16AsFloat32(result, outInt16)
	return result[:written]
}

// ProcessInto resamples float32 samples from input rate to output rate into a caller-provided buffer.
// This is the zero-allocation version of Process().
// Returns the number of samples written to the output buffer.
func (r *LibopusResampler) ProcessInto(samples []float32, out []float32) int {
	if len(samples) == 0 {
		return 0
	}
	if r.down != nil {
		return r.down.ProcessInto(samples, out)
	}

	in, inLen := r.prepareInputFromFloat32(samples)
	outInt16 := r.processInt16Core(in, inLen)
	written := writeInt16AsFloat32(out, outInt16)
	return written
}

// ResampleStereoInt16 runs silk_resampler on the left channel with l and on
// the right channel with r (each padded to 1 ms like ProcessInt16Into) and
// returns their int16 outputs, which stay valid until the resamplers' next
// calls. ok is false, and neither resampler runs, for empty input or down_FIR
// configurations, whose output lives in the delegated resampler; callers use
// ProcessInt16Into for those.
func ResampleStereoInt16(l, r *LibopusResampler, left, right []int16) (outL, outR []int16, ok bool) {
	if l.down != nil || r.down != nil || len(left) == 0 || len(right) == 0 {
		return nil, nil, false
	}
	in, inLen := l.prepareInputFromInt16(left)
	outL = l.processInt16Core(in, inLen)
	in, inLen = r.prepareInputFromInt16(right)
	outR = r.processInt16Core(in, inLen)
	return outL, outR, true
}

// InterleaveInt16AsFloat32 writes the INT16TORES conversion of left[i] and
// right[i] to dst[2i] and dst[2i+1] for i < len(left), as silk_Decode's stereo
// output loop does.
func InterleaveInt16AsFloat32(dst []float32, left, right []int16) {
	const inv32768 = 1.0 / 32768.0
	right = right[:len(left)]
	dst = dst[:2*len(left)]
	for i, l := range left {
		pair := (*[2]float32)(dst[2*i : 2*i+2])
		pair[0] = float32(l) * inv32768
		pair[1] = float32(right[i]) * inv32768
	}
}

// ProcessIntoBoth resamples float32 input, writing the resampler output to outF32
// (identical to ProcessInto) and copying the native int16 resampler output to
// outI16. See ProcessInt16IntoBoth for why the int16 output is needed by the
// FIXED_POINT integer hybrid path.
func (r *LibopusResampler) ProcessIntoBoth(samples []float32, outF32 []float32, outI16 []int16) int {
	if len(samples) == 0 {
		return 0
	}
	if r.down != nil {
		written := r.down.ProcessInto(samples, outF32)
		copy(outI16, r.down.scratchOut[:written])
		return written
	}

	in, inLen := r.prepareInputFromFloat32(samples)
	outInt16 := r.processInt16Core(in, inLen)
	written := writeInt16AsFloat32(outF32, outInt16)
	n := min(written, len(outI16))
	copy(outI16[:n], outInt16[:n])
	return written
}

// ProcessInt16Into resamples int16 input samples into float32 output.
// This avoids float32->int16 conversion when the caller already has native int16 samples.
func (r *LibopusResampler) ProcessInt16Into(samples []int16, out []float32) int {
	if len(samples) == 0 {
		return 0
	}
	if r.down != nil {
		return r.down.ProcessInt16Into(samples, out)
	}

	in, inLen := r.prepareInputFromInt16(samples)
	outInt16 := r.processInt16Core(in, inLen)
	written := writeInt16AsFloat32(out, outInt16)
	return written
}

// ProcessInt16IntoBoth resamples int16 input samples, writing the resampler's
// int16 output to outF32 (as float32, identical to ProcessInt16Into) and also
// copying the native int16 resampler output to outI16. The int16 output is the
// pre-INT16TORES value libopus' FIXED_POINT silk_Decode emits before the
// `INT16TORES(x) = x << RES_SHIFT` opus_res conversion, so callers driving the
// integer hybrid path can recover the exact opus_res SILK lowband without a
// second resampler pass (which would corrupt the stateful delay buffers).
// Returns the number of samples written.
func (r *LibopusResampler) ProcessInt16IntoBoth(samples []int16, outF32 []float32, outI16 []int16) int {
	if len(samples) == 0 {
		return 0
	}
	if r.down != nil {
		written := r.down.ProcessInt16Into(samples, outF32)
		copy(outI16, r.down.scratchOut[:written])
		return written
	}

	in, inLen := r.prepareInputFromInt16(samples)
	outInt16 := r.processInt16Core(in, inLen)
	written := writeInt16AsFloat32(outF32, outInt16)
	n := min(written, len(outI16))
	copy(outI16[:n], outInt16[:n])
	return written
}

// up2HQ implements silk_resampler_private_up2_HQ.
// 2x upsampling using 3rd order allpass filters.
func (r *LibopusResampler) up2HQ(out []int16, in []int16) {
	n := len(in)
	if n == 0 {
		return
	}
	out = out[: 2*n : 2*n]
	up2HQCore(out, in[:n:n], &r.sIIR)
}

// Allpass coefficients of silkResamplerUp2HQ0/1 as constants, so the hot loop
// multiplies by immediates and keeps its six filter states in registers.
const (
	up2HQ00 int64 = 1746
	up2HQ01 int64 = 14986
	up2HQ02 int64 = 39083 - 65536
	up2HQ10 int64 = 6854
	up2HQ11 int64 = 25769
	up2HQ12 int64 = 55542 - 65536
)

func up2HQCoreGo(out []int16, in []int16, sIIR *[6]int32) {
	// Keep allpass filter state in locals during the hot loop.
	s0, s1, s2 := sIIR[0], sIIR[1], sIIR[2]
	s3, s4, s5 := sIIR[3], sIIR[4], sIIR[5]

	out = out[:2*len(in)]
	for k, x := range in {
		// Convert to Q10
		in32 := int32(x) << 10

		// First all-pass section for even output sample
		X := int32((int64(in32-s0) * up2HQ00) >> 16)
		out32_1 := s0 + X
		s0 = in32 + X

		// Second all-pass section for even output sample
		X = int32((int64(out32_1-s1) * up2HQ01) >> 16)
		out32_2 := s1 + X
		s1 = out32_1 + X

		// Third all-pass section for even output sample
		Y := out32_2 - s2
		X = Y + int32((int64(Y)*up2HQ02)>>16)
		evenOut := s2 + X
		s2 = out32_2 + X

		// First all-pass section for odd output sample
		X = int32((int64(in32-s3) * up2HQ10) >> 16)
		out32_1 = s3 + X
		s3 = in32 + X

		// Second all-pass section for odd output sample
		X = int32((int64(out32_1-s4) * up2HQ11) >> 16)
		out32_2 = s4 + X
		s4 = out32_1 + X

		// Third all-pass section for odd output sample
		Y = out32_2 - s5
		X = Y + int32((int64(Y)*up2HQ12)>>16)
		oddOut := s5 + X
		s5 = out32_2 + X

		// Convert back to int16 and store the output pair
		pair := out[2*k : 2*k+2]
		pair[0] = sat16RShiftRound10(evenOut)
		pair[1] = sat16RShiftRound10(oddOut)
	}

	sIIR[0], sIIR[1], sIIR[2] = s0, s1, s2
	sIIR[3], sIIR[4], sIIR[5] = s3, s4, s5
}

// firInterpol implements silk_resampler_private_IIR_FIR_INTERPOL.
// FIR interpolation using the 12-phase coefficient table.
func (r *LibopusResampler) firInterpol(out []int16, outIdx int, buf []int16, maxIndexQ16 int32) int {
	indexIncrQ16 := r.invRatioQ16
	if maxIndexQ16 <= 0 || indexIncrQ16 <= 0 || outIdx >= len(out) {
		return outIdx
	}

	// Number of interpolation points generated by:
	// for indexQ16 := 0; indexQ16 < maxIndexQ16; indexQ16 += indexIncrQ16
	nOut := int((maxIndexQ16 + indexIncrQ16 - 1) / indexIncrQ16)
	remain := len(out) - outIdx
	if nOut > remain {
		nOut = remain
	}
	if nOut <= 0 {
		return outIdx
	}

	if done := firInterpolVec(out[outIdx:outIdx+nOut], buf, indexIncrQ16); done > 0 {
		firInterpolGeneric(out[outIdx+done:outIdx+nOut], buf, int32(done)*indexIncrQ16, indexIncrQ16)
		return outIdx + nOut
	}

	switch indexIncrQ16 {
	case 21846: // 8 kHz -> 48 kHz: phases 0, 4, 8 per input step.
		return r.firInterpol21846(out, outIdx, buf, nOut)
	case 32768: // 12 kHz -> 48 kHz: phases 0, 6 per input step.
		return r.firInterpol32768(out, outIdx, buf, nOut)
	case 43691: // 16 kHz -> 48 kHz: phases 0, 8, 4 over two input steps.
		return r.firInterpol43691(out, outIdx, buf, nOut)
	case 65536: // 24 kHz -> 48 kHz: phase 0 only.
		return r.firInterpol65536(out, outIdx, buf, nOut)
	case 87382: // 16 kHz -> 24 kHz and 8 kHz -> 12 kHz: phases 0, 4, 8.
		if nOut <= firInterpol87382MaxOut {
			firInterpol87382(out[outIdx:outIdx+nOut], buf)
			return outIdx + nOut
		}
	}

	firInterpolGeneric(out[outIdx:outIdx+nOut], buf, 0, indexIncrQ16)
	return outIdx + nOut
}

// firInterpolGeneric is the silk_resampler_private_IIR_FIR_INTERPOL loop for
// any index increment, producing len(dst) outputs from indexQ16 onwards.
func firInterpolGeneric(dst []int16, buf []int16, indexQ16, indexIncrQ16 int32) {
	nOut := len(dst)
	if nOut == 0 {
		return
	}
	// BCE hint for the last tap read.
	lastIndexQ16 := indexQ16 + int32(nOut-1)*indexIncrQ16
	_ = buf[int(lastIndexQ16>>16)+7]

	for n := 0; n < nOut; n, indexQ16 = n+1, indexQ16+indexIncrQ16 {
		// Fractional position for table lookup (0..11), matching libopus smulwb(indexQ16&0xFFFF, 12).
		tableIndex := int((uint32(indexQ16&0xFFFF) * 12) >> 16)
		bufIdx := int(indexQ16 >> 16)

		// 8-tap symmetric FIR filter.
		coeffBase := tableIndex << 2
		mirrorBase := (11 - tableIndex) << 2
		_ = silkResamplerFracFIR12Flat[coeffBase+3]
		_ = silkResamplerFracFIR12Flat[mirrorBase+3]
		buf8 := buf[bufIdx : bufIdx+8]
		_ = buf8[7]
		resQ15 := int32(buf8[0]) * int32(silkResamplerFracFIR12Flat[coeffBase+0])
		resQ15 += int32(buf8[1]) * int32(silkResamplerFracFIR12Flat[coeffBase+1])
		resQ15 += int32(buf8[2]) * int32(silkResamplerFracFIR12Flat[coeffBase+2])
		resQ15 += int32(buf8[3]) * int32(silkResamplerFracFIR12Flat[coeffBase+3])
		resQ15 += int32(buf8[4]) * int32(silkResamplerFracFIR12Flat[mirrorBase+3])
		resQ15 += int32(buf8[5]) * int32(silkResamplerFracFIR12Flat[mirrorBase+2])
		resQ15 += int32(buf8[6]) * int32(silkResamplerFracFIR12Flat[mirrorBase+1])
		resQ15 += int32(buf8[7]) * int32(silkResamplerFracFIR12Flat[mirrorBase+0])

		dst[n] = sat16RShiftRound15(resQ15)
	}
}

func (r *LibopusResampler) firInterpol21846(out []int16, outIdx int, buf []int16, nOut int) int {
	dst := out[outIdx : outIdx+nOut]
	_ = dst[nOut-1]
	lastBufIdx := (nOut - 1) / 3
	_ = buf[lastBufIdx+7]

	firInterpol21846Core(dst, buf, nOut)
	return outIdx + nOut
}

func firInterpol21846CoreGo(dst []int16, buf []int16, nOut int) {
	groups := nOut / 3
	j := 0
	for idx := range groups {
		buf8 := buf[idx : idx+8]

		resQ15 := int32(buf8[0])*fir0c0 +
			int32(buf8[1])*fir0c1 +
			int32(buf8[2])*fir0c2 +
			int32(buf8[3])*fir0c3 +
			int32(buf8[4])*fir0c4 +
			int32(buf8[5])*fir0c5 +
			int32(buf8[6])*fir0c6 +
			int32(buf8[7])*fir0c7
		dst[j] = sat16RShiftRound15(resQ15)

		resQ15 = int32(buf8[0])*fir4c0 +
			int32(buf8[1])*fir4c1 +
			int32(buf8[2])*fir4c2 +
			int32(buf8[3])*fir4c3 +
			int32(buf8[4])*fir4c4 +
			int32(buf8[5])*fir4c5 +
			int32(buf8[6])*fir4c6 +
			int32(buf8[7])*fir4c7
		dst[j+1] = sat16RShiftRound15(resQ15)

		resQ15 = int32(buf8[0])*fir8c0 +
			int32(buf8[1])*fir8c1 +
			int32(buf8[2])*fir8c2 +
			int32(buf8[3])*fir8c3 +
			int32(buf8[4])*fir8c4 +
			int32(buf8[5])*fir8c5 +
			int32(buf8[6])*fir8c6 +
			int32(buf8[7])*fir8c7
		dst[j+2] = sat16RShiftRound15(resQ15)

		j += 3
	}

	if j < nOut {
		idx := groups
		buf8 := buf[idx : idx+8]
		resQ15 := int32(buf8[0])*fir0c0 +
			int32(buf8[1])*fir0c1 +
			int32(buf8[2])*fir0c2 +
			int32(buf8[3])*fir0c3 +
			int32(buf8[4])*fir0c4 +
			int32(buf8[5])*fir0c5 +
			int32(buf8[6])*fir0c6 +
			int32(buf8[7])*fir0c7
		dst[j] = sat16RShiftRound15(resQ15)
		j++
		if j < nOut {
			resQ15 = int32(buf8[0])*fir4c0 +
				int32(buf8[1])*fir4c1 +
				int32(buf8[2])*fir4c2 +
				int32(buf8[3])*fir4c3 +
				int32(buf8[4])*fir4c4 +
				int32(buf8[5])*fir4c5 +
				int32(buf8[6])*fir4c6 +
				int32(buf8[7])*fir4c7
			dst[j] = sat16RShiftRound15(resQ15)
		}
	}
}

func (r *LibopusResampler) firInterpol32768(out []int16, outIdx int, buf []int16, nOut int) int {
	dst := out[outIdx : outIdx+nOut]
	_ = dst[nOut-1]
	lastBufIdx := (nOut - 1) >> 1
	_ = buf[lastBufIdx+7]

	firInterpol32768Core(dst, buf, nOut)
	return outIdx + nOut
}

func firInterpol32768CoreGo(dst []int16, buf []int16, nOut int) {
	groups := nOut >> 1
	j := 0
	for idx := range groups {
		buf8 := buf[idx : idx+8]

		resQ15 := int32(buf8[0])*fir0c0 +
			int32(buf8[1])*fir0c1 +
			int32(buf8[2])*fir0c2 +
			int32(buf8[3])*fir0c3 +
			int32(buf8[4])*fir0c4 +
			int32(buf8[5])*fir0c5 +
			int32(buf8[6])*fir0c6 +
			int32(buf8[7])*fir0c7
		dst[j] = sat16RShiftRound15(resQ15)

		resQ15 = int32(buf8[0])*fir6c0 +
			int32(buf8[1])*fir6c1 +
			int32(buf8[2])*fir6c2 +
			int32(buf8[3])*fir6c3 +
			int32(buf8[4])*fir6c4 +
			int32(buf8[5])*fir6c5 +
			int32(buf8[6])*fir6c6 +
			int32(buf8[7])*fir6c7
		dst[j+1] = sat16RShiftRound15(resQ15)

		j += 2
	}

	if j < nOut {
		idx := groups
		buf8 := buf[idx : idx+8]
		resQ15 := int32(buf8[0])*fir0c0 +
			int32(buf8[1])*fir0c1 +
			int32(buf8[2])*fir0c2 +
			int32(buf8[3])*fir0c3 +
			int32(buf8[4])*fir0c4 +
			int32(buf8[5])*fir0c5 +
			int32(buf8[6])*fir0c6 +
			int32(buf8[7])*fir0c7
		dst[j] = sat16RShiftRound15(resQ15)
	}
}

func (r *LibopusResampler) firInterpol43691(out []int16, outIdx int, buf []int16, nOut int) int {
	dst := out[outIdx : outIdx+nOut]
	_ = dst[nOut-1]
	lastBufIdx := (2 * (nOut - 1)) / 3
	_ = buf[lastBufIdx+7]

	firInterpol43691Core(dst, buf, nOut)
	return outIdx + nOut
}

func firInterpol43691CoreGo(dst []int16, buf []int16, nOut int) {
	groups := nOut / 3
	j := 0
	for g := range groups {
		idx := g << 1
		buf8 := buf[idx : idx+8]

		resQ15 := int32(buf8[0])*fir0c0 +
			int32(buf8[1])*fir0c1 +
			int32(buf8[2])*fir0c2 +
			int32(buf8[3])*fir0c3 +
			int32(buf8[4])*fir0c4 +
			int32(buf8[5])*fir0c5 +
			int32(buf8[6])*fir0c6 +
			int32(buf8[7])*fir0c7
		dst[j] = sat16RShiftRound15(resQ15)

		resQ15 = int32(buf8[0])*fir8c0 +
			int32(buf8[1])*fir8c1 +
			int32(buf8[2])*fir8c2 +
			int32(buf8[3])*fir8c3 +
			int32(buf8[4])*fir8c4 +
			int32(buf8[5])*fir8c5 +
			int32(buf8[6])*fir8c6 +
			int32(buf8[7])*fir8c7
		dst[j+1] = sat16RShiftRound15(resQ15)

		idx++
		buf8 = buf[idx : idx+8]
		resQ15 = int32(buf8[0])*fir4c0 +
			int32(buf8[1])*fir4c1 +
			int32(buf8[2])*fir4c2 +
			int32(buf8[3])*fir4c3 +
			int32(buf8[4])*fir4c4 +
			int32(buf8[5])*fir4c5 +
			int32(buf8[6])*fir4c6 +
			int32(buf8[7])*fir4c7
		dst[j+2] = sat16RShiftRound15(resQ15)

		j += 3
	}

	if j < nOut {
		idx := groups << 1
		buf8 := buf[idx : idx+8]
		resQ15 := int32(buf8[0])*fir0c0 +
			int32(buf8[1])*fir0c1 +
			int32(buf8[2])*fir0c2 +
			int32(buf8[3])*fir0c3 +
			int32(buf8[4])*fir0c4 +
			int32(buf8[5])*fir0c5 +
			int32(buf8[6])*fir0c6 +
			int32(buf8[7])*fir0c7
		dst[j] = sat16RShiftRound15(resQ15)
		j++
		if j < nOut {
			resQ15 = int32(buf8[0])*fir8c0 +
				int32(buf8[1])*fir8c1 +
				int32(buf8[2])*fir8c2 +
				int32(buf8[3])*fir8c3 +
				int32(buf8[4])*fir8c4 +
				int32(buf8[5])*fir8c5 +
				int32(buf8[6])*fir8c6 +
				int32(buf8[7])*fir8c7
			dst[j] = sat16RShiftRound15(resQ15)
		}
	}
}

// firInterpol87382MaxOut bounds the outputs for which the 87382 step keeps
// the phase pattern of firInterpol87382: three steps advance the index by
// 4<<16 plus 2, and that drift moves no phase before output 3*2730.
const firInterpol87382MaxOut = 3 * 2730

// firInterpol87382 is silk_resampler_private_IIR_FIR_INTERPOL for
// index_increment_Q16 = 87382 (a 2/3 step on the 2x-upsampled signal).
// Output 3g+k reads buf[4g+k:] with phase 4k for k = 0, 1, 2, so each group
// of three outputs uses one ten-sample window.
func firInterpol87382(dst []int16, buf []int16) {
	groups := len(dst) / 3
	_ = buf[4*len(dst)/3+7]
	for g := range groups {
		b := (*[10]int16)(buf[4*g : 4*g+10])
		d := (*[3]int16)(dst[3*g : 3*g+3])
		d[0] = sat16RShiftRound15(int32(b[0])*fir0c0 + int32(b[1])*fir0c1 + int32(b[2])*fir0c2 + int32(b[3])*fir0c3 +
			int32(b[4])*fir0c4 + int32(b[5])*fir0c5 + int32(b[6])*fir0c6 + int32(b[7])*fir0c7)
		d[1] = sat16RShiftRound15(int32(b[1])*fir4c0 + int32(b[2])*fir4c1 + int32(b[3])*fir4c2 + int32(b[4])*fir4c3 +
			int32(b[5])*fir4c4 + int32(b[6])*fir4c5 + int32(b[7])*fir4c6 + int32(b[8])*fir4c7)
		d[2] = sat16RShiftRound15(int32(b[2])*fir8c0 + int32(b[3])*fir8c1 + int32(b[4])*fir8c2 + int32(b[5])*fir8c3 +
			int32(b[6])*fir8c4 + int32(b[7])*fir8c5 + int32(b[8])*fir8c6 + int32(b[9])*fir8c7)
	}
	for j := 3 * groups; j < len(dst); j++ {
		b := (*[8]int16)(buf[4*groups+j-3*groups : 4*groups+j-3*groups+8])
		switch j - 3*groups {
		case 0:
			dst[j] = sat16RShiftRound15(int32(b[0])*fir0c0 + int32(b[1])*fir0c1 + int32(b[2])*fir0c2 + int32(b[3])*fir0c3 +
				int32(b[4])*fir0c4 + int32(b[5])*fir0c5 + int32(b[6])*fir0c6 + int32(b[7])*fir0c7)
		default:
			dst[j] = sat16RShiftRound15(int32(b[0])*fir4c0 + int32(b[1])*fir4c1 + int32(b[2])*fir4c2 + int32(b[3])*fir4c3 +
				int32(b[4])*fir4c4 + int32(b[5])*fir4c5 + int32(b[6])*fir4c6 + int32(b[7])*fir4c7)
		}
	}
}

func (r *LibopusResampler) firInterpol65536(out []int16, outIdx int, buf []int16, nOut int) int {
	dst := out[outIdx : outIdx+nOut]
	_ = dst[nOut-1]
	_ = buf[nOut+6]

	for idx := range nOut {
		buf8 := buf[idx : idx+8]
		resQ15 := int32(buf8[0])*fir0c0 +
			int32(buf8[1])*fir0c1 +
			int32(buf8[2])*fir0c2 +
			int32(buf8[3])*fir0c3 +
			int32(buf8[4])*fir0c4 +
			int32(buf8[5])*fir0c5 +
			int32(buf8[6])*fir0c6 +
			int32(buf8[7])*fir0c7
		dst[idx] = sat16RShiftRound15(resQ15)
	}

	return outIdx + nOut
}

// Fixed-point arithmetic helpers matching libopus SigProc_FIX.h

// smulwb: (a * b[15:0]) >> 16, treating b as signed 16-bit
func smulwb(a, b int32) int32 {
	return silkSMULWB(a, b)
}

// smulww: (a * b) >> 16
func smulww(a, b int32) int32 {
	return silkSMULWW(a, b)
}

// sat16: saturate to 16-bit range.
func sat16(x int32) int16 {
	return silkSAT16(x)
}

// rshiftRound: (x + (1 << (shift-1))) >> shift with rounding.
func rshiftRound(x int32, shift int) int32 {
	return silkRSHIFT_ROUND(x, shift)
}

func sat16RShiftRound10(x int32) int16 {
	y := ((x >> 9) + 1) >> 1
	if uint32(y+32768) <= 65535 {
		return int16(y)
	}
	if y < 0 {
		return -32768
	}
	return 32767
}

func sat16RShiftRound15(x int32) int16 {
	y := ((x >> 14) + 1) >> 1
	if uint32(y+32768) <= 65535 {
		return int16(y)
	}
	if y < 0 {
		return -32768
	}
	return 32767
}

// min32 returns the minimum of two int32 values.
func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

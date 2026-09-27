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
	down      *DownsamplingResampler
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

// Coefficients for 2x upsampler allpass filters (from resampler_rom.h)
var (
	// Tables for 2x upsampler, high quality
	// Even samples: 3rd order allpass
	silkResamplerUp2HQ0 = [3]int16{1746, 14986, 39083 - 65536}
	// Odd samples: 3rd order allpass
	silkResamplerUp2HQ1 = [3]int16{6854, 25769, 55542 - 65536}
)

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
// in \ out  8  12  16  24  48
var delayMatrixDec = [3][5]int8{
	/*  8 */ {4, 0, 2, 0, 0},
	/* 12 */ {0, 9, 4, 7, 4},
	/* 16 */ {0, 3, 12, 7, 7},
}

// rateID converts sample rate to index: 8000->0, 12000->1, 16000->2, 24000->3, 48000->4
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
	r := &LibopusResampler{
		fsInKHz:  int32(fsIn / 1000),
		fsOutKHz: int32(fsOut / 1000),
	}

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
		if inIdx < 3 && outIdx < 5 {
			r.inputDelay = int32(delayMatrixDec[inIdx][outIdx])
		}
	}

	if fsOut < fsIn {
		r.down = newDownsamplingResampler(fsIn, fsOut, forEnc)
		return r
	}
	if fsOut == fsIn {
		r.copyMode = true
		r.delayBuf = make([]int16, r.fsInKHz)
		maxInputSamples := int(r.fsInKHz * resamplerMaxFrameMs)
		r.scratchIn = make([]int16, maxInputSamples)
		r.scratchOut = make([]int16, maxInputSamples)
		r.scratchResult = make([]float32, maxInputSamples)
		return r
	}
	if fsOut == fsIn*2 {
		r.up2HQMode = true
		r.delayBuf = make([]int16, r.fsInKHz)
		maxInputSamples := int(r.fsInKHz * resamplerMaxFrameMs)
		maxOutputSamples := int(r.fsOutKHz * resamplerMaxFrameMs)
		r.scratchIn = make([]int16, maxInputSamples)
		r.scratchOut = make([]int16, maxOutputSamples)
		r.scratchResult = make([]float32, maxOutputSamples)
		return r
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

	// Initialize delay buffer
	r.delayBuf = make([]int16, r.fsInKHz)

	// Pre-allocate scratch buffers for zero-allocation resampling.
	// Opus allows up to 60ms frames, so size for that worst case.
	// Max input: fsInKHz * 60
	// Max output: fsOutKHz * 60
	maxInputSamples := int(r.fsInKHz * resamplerMaxFrameMs)
	maxOutputSamples := int(r.fsOutKHz * resamplerMaxFrameMs)
	r.scratchBuf = make([]int16, 2*r.batchSize+resamplerOrderFIR12)
	r.scratchIn = make([]int16, maxInputSamples)
	r.scratchOut = make([]int16, maxOutputSamples)
	r.scratchResult = make([]float32, maxOutputSamples)

	return r
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

// ProcessIntoBoth resamples float32 input, writing the resampler output to outF32
// (identical to ProcessInto) and copying the native int16 resampler output to
// outI16. See ProcessInt16IntoBoth for why the int16 output is needed by the
// FIXED_POINT integer hybrid path.
func (r *LibopusResampler) ProcessIntoBoth(samples []float32, outF32 []float32, outI16 []int16) int {
	if len(samples) == 0 {
		return 0
	}
	if r.down != nil {
		return r.down.ProcessInto(samples, outF32)
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
		// The < 16 kHz API downsample path is not exercised by hybrid decode
		// (hybrid SILK is always WB -> >=16k API); fall back to float-only.
		return r.down.ProcessInt16Into(samples, outF32)
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

func up2HQCoreGo(out []int16, in []int16, sIIR *[6]int32) {
	// Keep allpass filter state in locals during the hot loop.
	s0, s1, s2 := sIIR[0], sIIR[1], sIIR[2]
	s3, s4, s5 := sIIR[3], sIIR[4], sIIR[5]

	c00 := int64(silkResamplerUp2HQ0[0])
	c01 := int64(silkResamplerUp2HQ0[1])
	c02 := int64(silkResamplerUp2HQ0[2])
	c10 := int64(silkResamplerUp2HQ1[0])
	c11 := int64(silkResamplerUp2HQ1[1])
	c12 := int64(silkResamplerUp2HQ1[2])

	_ = out[2*len(in)-1]

	outPos := 0
	for k := range in {
		// Convert to Q10
		in32 := int32(in[k]) << 10

		// First all-pass section for even output sample
		Y := in32 - s0
		X := int32((int64(Y) * c00) >> 16)
		out32_1 := s0 + X
		s0 = in32 + X

		// Second all-pass section for even output sample
		Y = out32_1 - s1
		X = int32((int64(Y) * c01) >> 16)
		out32_2 := s1 + X
		s1 = out32_1 + X

		// Third all-pass section for even output sample
		Y = out32_2 - s2
		X = Y + int32((int64(Y)*c02)>>16)
		out32_1 = s2 + X
		s2 = out32_2 + X

		// Convert back to int16 and store even sample
		out[outPos] = sat16RShiftRound10(out32_1)

		// First all-pass section for odd output sample
		Y = in32 - s3
		X = int32((int64(Y) * c10) >> 16)
		out32_1 = s3 + X
		s3 = in32 + X

		// Second all-pass section for odd output sample
		Y = out32_1 - s4
		X = int32((int64(Y) * c11) >> 16)
		out32_2 = s4 + X
		s4 = out32_1 + X

		// Third all-pass section for odd output sample
		Y = out32_2 - s5
		X = Y + int32((int64(Y)*c12)>>16)
		out32_1 = s5 + X
		s5 = out32_2 + X

		// Convert back to int16 and store odd sample
		out[outPos+1] = sat16RShiftRound10(out32_1)
		outPos += 2
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

	switch indexIncrQ16 {
	case 21846: // 8 kHz -> 48 kHz: phases 0, 4, 8 per input step.
		return r.firInterpol21846(out, outIdx, buf, nOut)
	case 32768: // 12 kHz -> 48 kHz: phases 0, 6 per input step.
		return r.firInterpol32768(out, outIdx, buf, nOut)
	case 43691: // 16 kHz -> 48 kHz: phases 0, 8, 4 over two input steps.
		return r.firInterpol43691(out, outIdx, buf, nOut)
	case 65536: // 24 kHz -> 48 kHz: phase 0 only.
		return r.firInterpol65536(out, outIdx, buf, nOut)
	}

	// BCE hints for hot inner-loop accesses.
	_ = out[outIdx+nOut-1]
	lastIndexQ16 := int32(nOut-1) * indexIncrQ16
	_ = buf[int(lastIndexQ16>>16)+7]

	dst := out[outIdx : outIdx+nOut]
	_ = dst[nOut-1]
	for indexQ16, n := int32(0), 0; n < nOut; n, indexQ16 = n+1, indexQ16+indexIncrQ16 {
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

	return outIdx + nOut
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

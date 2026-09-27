package silk

// Downsampling resampler using AR2 filter followed by FIR interpolation.
// This ports libopus silk_resampler_private_down_FIR for encoder downsampling.
// Reference: libopus silk/resampler.c, silk/resampler_private_down_FIR.c

// Resampler FIR order constants
const (
	resamplerDownOrderFIR0 = 18 // For 3:4 and 2:3 ratios
	resamplerDownOrderFIR1 = 24 // For 1:2 ratio
	resamplerDownOrderFIR2 = 36 // For 1:3, 1:4, 1:6 ratios
)

// Encoder delay matrix from libopus resampler.c
// in  \ out  8  12  16
var delayMatrixEnc = [6][3]int8{
	/*  8 */ {6, 0, 3},
	/* 12 */ {0, 7, 3},
	/* 16 */ {0, 1, 10},
	/* 24 */ {0, 2, 6},
	/* 48 */ {18, 10, 12},
	/* 96 */ {0, 0, 44},
}

// FIR coefficients for downsampling from libopus resampler_rom.c
// Format: [2 AR2 coefficients] + [symmetric FIR coefficients]

// silk_Resampler_3_4_COEFS: 48kHz -> 36kHz (3:4 ratio)
var silkResampler34Coefs = []int16{
	-20694, -13867, // AR2 coefficients
	-49, 64, 17, -157, 353, -496, 163, 11047, 22205, // Phase 0
	-39, 6, 91, -170, 186, 23, -896, 6336, 19928, // Phase 1
	-19, -36, 102, -89, -24, 328, -951, 2568, 15909, // Phase 2
}

// silk_Resampler_2_3_COEFS: 48kHz -> 32kHz, 24kHz -> 16kHz (2:3 ratio)
var silkResampler23Coefs = []int16{
	-14457, -14019, // AR2 coefficients
	64, 128, -122, 36, 310, -768, 584, 9267, 17733, // Phase 0
	12, 128, 18, -142, 288, -117, -865, 4123, 14459, // Phase 1
}

// silk_Resampler_1_2_COEFS: 48kHz -> 24kHz (1:2 ratio)
var silkResampler12Coefs = []int16{
	616, -14323, // AR2 coefficients
	-10, 39, 58, -46, -84, 120, 184, -315, -541, 1284, 5380, 9024, // Symmetric FIR
}

// silk_Resampler_1_3_COEFS: 48kHz -> 16kHz (1:3 ratio) - MOST COMMON FOR SILK ENCODER
var silkResampler13Coefs = []int16{
	16102, -15162, // AR2 coefficients
	-13, 0, 20, 26, 5, -31, -43, -4, 65, 90, 7, -157, -248, -44, 593, 1583, 2612, 3271, // Symmetric FIR
}

// silk_Resampler_1_4_COEFS: 48kHz -> 12kHz (1:4 ratio)
var silkResampler14Coefs = []int16{
	22500, -15099, // AR2 coefficients
	3, -14, -20, -15, 2, 25, 37, 25, -16, -71, -107, -79, 50, 292, 623, 982, 1288, 1464, // Symmetric FIR
}

// silk_Resampler_1_6_COEFS: 48kHz -> 8kHz (1:6 ratio)
var silkResampler16Coefs = []int16{
	27540, -15257, // AR2 coefficients
	17, 12, 8, 1, -10, -22, -30, -32, -22, 3, 44, 100, 168, 243, 317, 381, 429, 455, // Symmetric FIR
}

// DownsamplingResampler implements the libopus down_FIR resampling algorithm.
// This is used for encoder mode (48kHz -> SILK rates).
type DownsamplingResampler struct {
	// AR2 IIR filter state (2 elements)
	sIIR [2]int32

	// FIR filter state (size depends on FIR order)
	sFIR []int32

	// Configuration
	fsInKHz     int32
	fsOutKHz    int32
	batchSize   int32
	inputDelay  int32
	invRatioQ16 int32
	firOrder    int
	firFracs    int
	coefs       []int16 // Full coefficient array (AR2 + FIR)
	firCoefs    []int16 // Just the FIR coefficients (after AR2)

	// Delay buffer
	delayBuf []int16

	// Scratch buffers
	scratchBuf []int32
	scratchIn  []int16 // ProcessInto: input int16 conversion
	scratchOut []int16 // ProcessInto: output int16 buffer
}

// DownsamplingResamplerState holds the internal state of the downsampling resampler.
type DownsamplingResamplerState struct {
	sIIR     [2]int32
	sFIR     []int32
	delayBuf []int16
}

// State returns a snapshot of the current resampler state.
func (r *DownsamplingResampler) State() DownsamplingResamplerState {
	s := DownsamplingResamplerState{
		sIIR: r.sIIR,
	}
	if len(r.sFIR) > 0 {
		s.sFIR = make([]int32, len(r.sFIR))
		copy(s.sFIR, r.sFIR)
	}
	if len(r.delayBuf) > 0 {
		s.delayBuf = make([]int16, len(r.delayBuf))
		copy(s.delayBuf, r.delayBuf)
	}
	return s
}

// SetState restores the resampler state from a snapshot.
func (r *DownsamplingResampler) SetState(s DownsamplingResamplerState) {
	r.sIIR = s.sIIR
	clear(r.sFIR)
	if len(s.sFIR) > 0 && len(r.sFIR) >= len(s.sFIR) {
		copy(r.sFIR, s.sFIR)
	}
	clear(r.delayBuf)
	if len(s.delayBuf) > 0 && len(r.delayBuf) >= len(s.delayBuf) {
		copy(r.delayBuf, s.delayBuf)
	}
}

// NewDownsamplingResampler creates a new downsampling resampler for encoder mode.
// Supports: 48kHz -> 8/12/16 kHz, 24kHz -> 8/12/16 kHz, etc.
func NewDownsamplingResampler(fsIn, fsOut int) *DownsamplingResampler {
	return newDownsamplingResampler(fsIn, fsOut, true)
}

func newDecoderDownsamplingResampler(fsIn, fsOut int) *DownsamplingResampler {
	return newDownsamplingResampler(fsIn, fsOut, false)
}

func newDownsamplingResampler(fsIn, fsOut int, forEncoder bool) *DownsamplingResampler {
	r := &DownsamplingResampler{
		fsInKHz:  int32(fsIn / 1000),
		fsOutKHz: int32(fsOut / 1000),
	}

	// Batch size: 10ms of input data
	r.batchSize = r.fsInKHz * 10 // RESAMPLER_MAX_BATCH_SIZE_MS = 10

	if forEncoder {
		inIdx := rateIDEnc(fsIn)
		outIdx := rateIDEnc(fsOut)
		if inIdx >= 0 && inIdx < 6 && outIdx >= 0 && outIdx < 3 {
			r.inputDelay = int32(delayMatrixEnc[inIdx][outIdx])
		}
	} else {
		inIdx := rateID(fsIn)
		outIdx := rateID(fsOut)
		if inIdx >= 0 && inIdx < 3 && outIdx >= 0 && outIdx < 5 {
			r.inputDelay = int32(delayMatrixDec[inIdx][outIdx])
		}
	}

	// Select coefficients based on ratio
	fsInHz := int32(fsIn)
	fsOutHz := int32(fsOut)

	if fsOutHz*4 == fsInHz*3 { // 3:4 ratio (48kHz -> 36kHz)
		r.firFracs = 3
		r.firOrder = resamplerDownOrderFIR0
		r.coefs = silkResampler34Coefs
	} else if fsOutHz*3 == fsInHz*2 { // 2:3 ratio (48kHz -> 32kHz, 24kHz -> 16kHz)
		r.firFracs = 2
		r.firOrder = resamplerDownOrderFIR0
		r.coefs = silkResampler23Coefs
	} else if fsOutHz*2 == fsInHz { // 1:2 ratio (48kHz -> 24kHz)
		r.firFracs = 1
		r.firOrder = resamplerDownOrderFIR1
		r.coefs = silkResampler12Coefs
	} else if fsOutHz*3 == fsInHz { // 1:3 ratio (48kHz -> 16kHz) - MOST COMMON
		r.firFracs = 1
		r.firOrder = resamplerDownOrderFIR2
		r.coefs = silkResampler13Coefs
	} else if fsOutHz*4 == fsInHz { // 1:4 ratio (48kHz -> 12kHz)
		r.firFracs = 1
		r.firOrder = resamplerDownOrderFIR2
		r.coefs = silkResampler14Coefs
	} else if fsOutHz*6 == fsInHz { // 1:6 ratio (48kHz -> 8kHz)
		r.firFracs = 1
		r.firOrder = resamplerDownOrderFIR2
		r.coefs = silkResampler16Coefs
	} else {
		// Unsupported ratio - use 1:3 as fallback
		r.firFracs = 1
		r.firOrder = resamplerDownOrderFIR2
		r.coefs = silkResampler13Coefs
	}

	// FIR coefficients start after 2 AR2 coefficients
	r.firCoefs = r.coefs[2:]

	// Compute invRatio_Q16 (up2x = 0 for downsampling)
	r.invRatioQ16 = int32((int64(fsInHz) << 16) / int64(fsOutHz))
	// Round up
	for int32((int64(r.invRatioQ16)*int64(fsOutHz))>>16) < int32(fsInHz) {
		r.invRatioQ16++
	}

	// Initialize state
	r.sFIR = make([]int32, r.firOrder)
	r.delayBuf = make([]int16, r.fsInKHz)
	r.scratchBuf = make([]int32, int(r.batchSize)+r.firOrder)

	return r
}

// CopyFrom copies src's configuration and filter state into r, leaving r ready
// to continue resampling exactly as src would. It is a no-op if either receiver
// or src is nil.
func (r *DownsamplingResampler) CopyFrom(src *DownsamplingResampler) {
	if r == nil || src == nil {
		return
	}
	r.sIIR = src.sIIR
	r.fsInKHz = src.fsInKHz
	r.fsOutKHz = src.fsOutKHz
	r.batchSize = src.batchSize
	r.inputDelay = src.inputDelay
	r.invRatioQ16 = src.invRatioQ16
	r.firOrder = src.firOrder
	r.firFracs = src.firFracs
	r.coefs = src.coefs
	r.firCoefs = src.firCoefs
	if len(r.sFIR) != len(src.sFIR) {
		r.sFIR = make([]int32, len(src.sFIR))
	}
	copy(r.sFIR, src.sFIR)
	if len(r.delayBuf) != len(src.delayBuf) {
		r.delayBuf = make([]int16, len(src.delayBuf))
	}
	copy(r.delayBuf, src.delayBuf)
}

// rateIDEnc converts sample rate to index for encoder delay matrix
func rateIDEnc(rate int) int {
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
		return -1
	}
}

// Process resamples input samples and returns output samples.
func (r *DownsamplingResampler) Process(in []float32) []float32 {
	// Convert float32 to int16
	inInt := make([]int16, len(in))
	for i, v := range in {
		inInt[i] = float32ToInt16(v)
	}
	if len(inInt) < int(r.fsInKHz) {
		padded := make([]int16, r.fsInKHz)
		copy(padded, inInt)
		inInt = padded
	}

	// Calculate output length
	outLen := int(int64(len(in)) * int64(r.fsOutKHz) / int64(r.fsInKHz))
	outInt := make([]int16, outLen)

	// Process with libopus-style delay handling
	r.processWithDelay(outInt, inInt)

	// Convert back to float32
	out := make([]float32, len(outInt))
	for i, v := range outInt {
		out[i] = float32(v) / 32768.0
	}
	return out
}

// ProcessInto resamples into a pre-allocated buffer.
func (r *DownsamplingResampler) ProcessInto(in []float32, out []float32) int {
	// Convert float32 to int16 using scratch buffer
	inNeeded := max(len(in), int(r.fsInKHz))
	if cap(r.scratchIn) < inNeeded {
		r.scratchIn = make([]int16, inNeeded)
	}
	inInt := r.scratchIn[:inNeeded]
	for i, v := range in {
		inInt[i] = float32ToInt16(v)
	}
	if len(in) < int(r.fsInKHz) {
		clear(inInt[len(in):])
	}

	// Calculate output length
	outLen := min(int(int64(len(in))*int64(r.fsOutKHz)/int64(r.fsInKHz)), len(out))
	if cap(r.scratchOut) < outLen {
		r.scratchOut = make([]int16, outLen)
	}
	outInt := r.scratchOut[:outLen]

	// Process with libopus-style delay handling
	r.processWithDelay(outInt, inInt)

	// Convert back to float32
	for i := 0; i < outLen && i < len(outInt); i++ {
		out[i] = float32(outInt[i]) / 32768.0
	}
	return outLen
}

// ProcessInt16Into resamples int16 input into the pre-allocated float32 buffer
// out and returns the number of samples written. It mirrors ProcessInto but
// takes the input directly as int16, avoiding a float conversion on the way in.
func (r *DownsamplingResampler) ProcessInt16Into(in []int16, out []float32) int {
	if len(in) == 0 {
		return 0
	}
	inLen := len(in)
	inNeeded := max(inLen, int(r.fsInKHz))
	var inInt []int16
	if inNeeded == len(in) {
		inInt = in
	} else {
		if cap(r.scratchIn) < inNeeded {
			r.scratchIn = make([]int16, inNeeded)
		}
		inInt = r.scratchIn[:inNeeded]
		clear(inInt)
		copy(inInt, in)
		inLen = inNeeded
	}

	outLen := min(int(int64(inLen)*int64(r.fsOutKHz)/int64(r.fsInKHz)), len(out))
	if cap(r.scratchOut) < outLen {
		r.scratchOut = make([]int16, outLen)
	}
	outInt := r.scratchOut[:outLen]
	r.processWithDelay(outInt, inInt)
	return writeInt16AsFloat32(out, outInt)
}

// processWithDelay matches libopus silk_resampler() for encoder downsampling.
// It applies input delay buffering before calling the down_FIR core.
func (r *DownsamplingResampler) processWithDelay(out []int16, in []int16) {
	inLen := len(in)
	if inLen == 0 {
		return
	}

	nSamples := min(max(int(r.fsInKHz-r.inputDelay), 0), inLen)

	// Copy to delay buffer (preserve first inputDelay samples from previous call).
	if nSamples > 0 {
		copy(r.delayBuf[r.inputDelay:], in[:nSamples])
	}

	// Process delay buffer (1ms = fsInKHz samples).
	if len(out) >= int(r.fsOutKHz) {
		r.processInt16(out[:r.fsOutKHz], r.delayBuf[:r.fsInKHz])
	}

	// Process remaining input, excluding the last inputDelay samples.
	if inLen > int(r.fsInKHz) && len(out) > int(r.fsOutKHz) {
		end := min(max(inLen-int(r.inputDelay), nSamples), inLen)
		r.processInt16(out[r.fsOutKHz:], in[nSamples:end])
	}

	// Save last inputDelay samples to delay buffer.
	if r.inputDelay > 0 && inLen >= int(r.inputDelay) {
		copy(r.delayBuf[:r.inputDelay], in[inLen-int(r.inputDelay):])
	}
}

// processInt16 is the core downsampling function.
// Matches libopus silk_resampler_private_down_FIR exactly.
func (r *DownsamplingResampler) processInt16(out []int16, in []int16) {
	inLen := int32(len(in))
	outIdx := 0

	// Ensure scratch buffer is large enough
	bufSize := int(r.batchSize) + r.firOrder
	if len(r.scratchBuf) < bufSize {
		r.scratchBuf = make([]int32, bufSize)
	}
	buf := r.scratchBuf

	// Copy FIR state (buffered samples) to start of buffer
	copy(buf, r.sFIR)

	inOffset := int32(0)
	indexIncrementQ16 := r.invRatioQ16
	var lastNSamplesIn int32

	for {
		nSamplesIn := min32(inLen, r.batchSize)
		lastNSamplesIn = nSamplesIn

		// Apply AR2 filter (output in Q8)
		r.ar2Filter(buf[r.firOrder:], in[inOffset:inOffset+nSamplesIn])

		// Interpolate filtered signal
		maxIndexQ16 := nSamplesIn << 16
		outIdx = r.firInterpolate(out[outIdx:], buf, maxIndexQ16, indexIncrementQ16, outIdx)

		inOffset += nSamplesIn
		inLen -= nSamplesIn

		if inLen > 1 {
			// More iterations to do; copy last part of filtered signal to beginning of buffer
			copy(buf, buf[nSamplesIn:nSamplesIn+int32(r.firOrder)])
		} else {
			break
		}
	}

	// Save FIR state for next call (from the last nSamplesIn position)
	copy(r.sFIR, buf[lastNSamplesIn:lastNSamplesIn+int32(r.firOrder)])
}

// ar2Filter applies the second-order AR pre-filter.
// Output is in Q8 format.
// Matches libopus silk_resampler_private_AR2 exactly.
//
// Reference: silk/resampler_private_AR2.c
//
//	for( k = 0; k < len; k++ ) {
//	    out32       = silk_ADD_LSHIFT32( S[ 0 ], (opus_int32)in[ k ], 8 );
//	    out_Q8[ k ] = out32;
//	    out32       = silk_LSHIFT( out32, 2 );
//	    S[ 0 ]      = silk_SMLAWB( S[ 1 ], out32, A_Q14[ 0 ] );
//	    S[ 1 ]      = silk_SMULWB( out32, A_Q14[ 1 ] );
//	}
func (r *DownsamplingResampler) ar2Filter(out []int32, in []int16) {
	A0Q14 := int32(r.coefs[0]) // Q14 coefficient
	A1Q14 := int32(r.coefs[1]) // Q14 coefficient
	n := len(in)
	if n == 0 {
		return
	}
	_ = in[n-1]  // BCE hint
	_ = out[n-1] // BCE hint

	// Pre-cast coefficients to int64 for inlined SMULWB.
	a0 := int64(int16(A0Q14))
	a1 := int64(int16(A1Q14))
	s0, s1 := r.sIIR[0], r.sIIR[1]
	for k := range n {
		out32 := s0 + (int32(in[k]) << 8)
		out[k] = out32
		out32 <<= 2
		s0 = s1 + int32((int64(out32)*a0)>>16)
		s1 = int32((int64(out32) * a1) >> 16)
	}
	r.sIIR[0], r.sIIR[1] = s0, s1
}

// firInterpolate performs FIR interpolation on the filtered signal.
// Matches libopus silk_resampler_private_down_FIR_INTERPOL.
func (r *DownsamplingResampler) firInterpolate(out []int16, buf []int32, maxIndexQ16, indexIncrementQ16 int32, startOutIdx int) int {
	outIdx := 0

	switch r.firOrder {
	case resamplerDownOrderFIR0:
		// 18-tap filter with multiple phases
		firFracs := r.firFracs
		firCoefs := r.firCoefs
		for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += indexIncrementQ16 {
			bufPtr := int(indexQ16 >> 16)
			_ = buf[bufPtr+17] // BCE hint
			interpolInd := int(smulwb(indexQ16&0xFFFF, int32(firFracs)))

			// Forward taps
			interpol := firCoefs[resamplerDownOrderFIR0/2*interpolInd:]
			_ = interpol[8] // BCE hint
			f0, f1, f2, f3 := int64(int16(interpol[0])), int64(int16(interpol[1])), int64(int16(interpol[2])), int64(int16(interpol[3]))
			f4, f5, f6, f7, f8 := int64(int16(interpol[4])), int64(int16(interpol[5])), int64(int16(interpol[6])), int64(int16(interpol[7])), int64(int16(interpol[8]))
			resQ6 := int32((int64(buf[bufPtr+0]) * f0) >> 16)
			resQ6 += int32((int64(buf[bufPtr+1]) * f1) >> 16)
			resQ6 += int32((int64(buf[bufPtr+2]) * f2) >> 16)
			resQ6 += int32((int64(buf[bufPtr+3]) * f3) >> 16)
			resQ6 += int32((int64(buf[bufPtr+4]) * f4) >> 16)
			resQ6 += int32((int64(buf[bufPtr+5]) * f5) >> 16)
			resQ6 += int32((int64(buf[bufPtr+6]) * f6) >> 16)
			resQ6 += int32((int64(buf[bufPtr+7]) * f7) >> 16)
			resQ6 += int32((int64(buf[bufPtr+8]) * f8) >> 16)

			// Reverse taps (symmetric filter)
			interpol = firCoefs[resamplerDownOrderFIR0/2*(firFracs-1-interpolInd):]
			_ = interpol[8] // BCE hint
			r0, r1, r2, r3 := int64(int16(interpol[0])), int64(int16(interpol[1])), int64(int16(interpol[2])), int64(int16(interpol[3]))
			r4, r5, r6, r7, r8 := int64(int16(interpol[4])), int64(int16(interpol[5])), int64(int16(interpol[6])), int64(int16(interpol[7])), int64(int16(interpol[8]))
			resQ6 += int32((int64(buf[bufPtr+17]) * r0) >> 16)
			resQ6 += int32((int64(buf[bufPtr+16]) * r1) >> 16)
			resQ6 += int32((int64(buf[bufPtr+15]) * r2) >> 16)
			resQ6 += int32((int64(buf[bufPtr+14]) * r3) >> 16)
			resQ6 += int32((int64(buf[bufPtr+13]) * r4) >> 16)
			resQ6 += int32((int64(buf[bufPtr+12]) * r5) >> 16)
			resQ6 += int32((int64(buf[bufPtr+11]) * r6) >> 16)
			resQ6 += int32((int64(buf[bufPtr+10]) * r7) >> 16)
			resQ6 += int32((int64(buf[bufPtr+9]) * r8) >> 16)

			if outIdx < len(out) {
				out[outIdx] = int16(sat16(rshiftRound(resQ6, 6)))
				outIdx++
			}
		}

	case resamplerDownOrderFIR1:
		// 24-tap symmetric filter (single phase)
		// Cache FIR coefficients as int64 to avoid repeated int32->int64 conversion.
		fc1 := r.firCoefs
		_ = fc1[11] // BCE hint
		d0, d1, d2, d3 := int64(int16(fc1[0])), int64(int16(fc1[1])), int64(int16(fc1[2])), int64(int16(fc1[3]))
		d4, d5, d6, d7 := int64(int16(fc1[4])), int64(int16(fc1[5])), int64(int16(fc1[6])), int64(int16(fc1[7]))
		d8, d9, d10, d11 := int64(int16(fc1[8])), int64(int16(fc1[9])), int64(int16(fc1[10])), int64(int16(fc1[11]))
		for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += indexIncrementQ16 {
			bufPtr := int(indexQ16 >> 16)
			_ = buf[bufPtr+23] // BCE hint

			resQ6 := int32((int64(buf[bufPtr+0]+buf[bufPtr+23]) * d0) >> 16)
			resQ6 += int32((int64(buf[bufPtr+1]+buf[bufPtr+22]) * d1) >> 16)
			resQ6 += int32((int64(buf[bufPtr+2]+buf[bufPtr+21]) * d2) >> 16)
			resQ6 += int32((int64(buf[bufPtr+3]+buf[bufPtr+20]) * d3) >> 16)
			resQ6 += int32((int64(buf[bufPtr+4]+buf[bufPtr+19]) * d4) >> 16)
			resQ6 += int32((int64(buf[bufPtr+5]+buf[bufPtr+18]) * d5) >> 16)
			resQ6 += int32((int64(buf[bufPtr+6]+buf[bufPtr+17]) * d6) >> 16)
			resQ6 += int32((int64(buf[bufPtr+7]+buf[bufPtr+16]) * d7) >> 16)
			resQ6 += int32((int64(buf[bufPtr+8]+buf[bufPtr+15]) * d8) >> 16)
			resQ6 += int32((int64(buf[bufPtr+9]+buf[bufPtr+14]) * d9) >> 16)
			resQ6 += int32((int64(buf[bufPtr+10]+buf[bufPtr+13]) * d10) >> 16)
			resQ6 += int32((int64(buf[bufPtr+11]+buf[bufPtr+12]) * d11) >> 16)

			if outIdx < len(out) {
				out[outIdx] = int16(sat16(rshiftRound(resQ6, 6)))
				outIdx++
			}
		}

	case resamplerDownOrderFIR2:
		// 36-tap symmetric filter (single phase) - MOST COMMON (48kHz -> 16kHz)
		// Cache FIR coefficients as int64 for inlined smlawb (avoids repeated int32->int64 conversion).
		fc := r.firCoefs
		_ = fc[17] // BCE hint
		c0, c1, c2, c3 := int64(int16(fc[0])), int64(int16(fc[1])), int64(int16(fc[2])), int64(int16(fc[3]))
		c4, c5, c6, c7 := int64(int16(fc[4])), int64(int16(fc[5])), int64(int16(fc[6])), int64(int16(fc[7]))
		c8, c9, c10, c11 := int64(int16(fc[8])), int64(int16(fc[9])), int64(int16(fc[10])), int64(int16(fc[11]))
		c12, c13, c14, c15 := int64(int16(fc[12])), int64(int16(fc[13])), int64(int16(fc[14])), int64(int16(fc[15]))
		c16, c17 := int64(int16(fc[16])), int64(int16(fc[17]))
		for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += indexIncrementQ16 {
			bufPtr := int(indexQ16 >> 16)
			_ = buf[bufPtr+35] // BCE hint: prove all 36 elements are in bounds

			// Inline smlawb: a + ((int64(b) * c) >> 16) where c is pre-cast to int64.
			resQ6 := int32((int64(buf[bufPtr+0]+buf[bufPtr+35]) * c0) >> 16)
			resQ6 += int32((int64(buf[bufPtr+1]+buf[bufPtr+34]) * c1) >> 16)
			resQ6 += int32((int64(buf[bufPtr+2]+buf[bufPtr+33]) * c2) >> 16)
			resQ6 += int32((int64(buf[bufPtr+3]+buf[bufPtr+32]) * c3) >> 16)
			resQ6 += int32((int64(buf[bufPtr+4]+buf[bufPtr+31]) * c4) >> 16)
			resQ6 += int32((int64(buf[bufPtr+5]+buf[bufPtr+30]) * c5) >> 16)
			resQ6 += int32((int64(buf[bufPtr+6]+buf[bufPtr+29]) * c6) >> 16)
			resQ6 += int32((int64(buf[bufPtr+7]+buf[bufPtr+28]) * c7) >> 16)
			resQ6 += int32((int64(buf[bufPtr+8]+buf[bufPtr+27]) * c8) >> 16)
			resQ6 += int32((int64(buf[bufPtr+9]+buf[bufPtr+26]) * c9) >> 16)
			resQ6 += int32((int64(buf[bufPtr+10]+buf[bufPtr+25]) * c10) >> 16)
			resQ6 += int32((int64(buf[bufPtr+11]+buf[bufPtr+24]) * c11) >> 16)
			resQ6 += int32((int64(buf[bufPtr+12]+buf[bufPtr+23]) * c12) >> 16)
			resQ6 += int32((int64(buf[bufPtr+13]+buf[bufPtr+22]) * c13) >> 16)
			resQ6 += int32((int64(buf[bufPtr+14]+buf[bufPtr+21]) * c14) >> 16)
			resQ6 += int32((int64(buf[bufPtr+15]+buf[bufPtr+20]) * c15) >> 16)
			resQ6 += int32((int64(buf[bufPtr+16]+buf[bufPtr+19]) * c16) >> 16)
			resQ6 += int32((int64(buf[bufPtr+17]+buf[bufPtr+18]) * c17) >> 16)

			if outIdx < len(out) {
				out[outIdx] = int16(sat16(rshiftRound(resQ6, 6)))
				outIdx++
			}
		}
	}

	return startOutIdx + outIdx
}

// Helper functions for fixed-point arithmetic
// Note: These use the existing functions from resample_libopus.go:
// smulww, smulwb, smlawb, sat16, rshiftRound

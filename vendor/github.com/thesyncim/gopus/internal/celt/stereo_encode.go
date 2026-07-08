// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides stereo mode encoding for the CELT encoder.

package celt

// IntensityDecay is the decay parameter for intensity stereo Laplace encoding.
// Matches the decoder's expectation for stereo param decoding.
// Reference: libopus celt/celt_decoder.c, stereo parameter decoding
const IntensityDecay = 16384

// EncodeStereoParams encodes stereo mode parameters to the bitstream.
// For the initial implementation, this encodes mid-side stereo only:
// - intensity = nbBands (meaning no intensity stereo, all bands use mid-side)
// - dual_stereo = 0 (meaning mid-side mode, not dual stereo)
//
// Returns the intensity band (-1 since intensity stereo is disabled in this mode).
//
// The decoder reads stereo params in decodeStereoParams() which expects:
// 1. intensity band index encoded with Laplace model
// 2. dual_stereo flag encoded as single bit
//
// Reference: RFC 6716 Section 4.3.4, libopus celt/celt_decoder.c
func (e *Encoder) EncodeStereoParams(nbBands int) int {
	if e.rangeEncoder == nil {
		return -1
	}

	// For dual stereo mode (simpler, encoding L and R independently):
	// intensity = nbBands (disabled)
	// dual_stereo = 1 (enabled)
	e.encodeLaplaceIntensity(nbBands, IntensityDecay)

	// dual_stereo = 1 means "use dual stereo"
	// Encoded as a single bit with 50% probability
	e.rangeEncoder.EncodeBit(1, 1)

	// Return -1 to indicate intensity stereo is disabled
	return -1
}

// encodeLaplaceIntensity encodes the intensity stereo band using Laplace model.
// This mirrors the decoder's decodeLaplace for stereo params.
// val is the intensity band (nbBands for mid-side only mode).
func (e *Encoder) encodeLaplaceIntensity(val int, decay int) {
	re := e.rangeEncoder
	if re == nil {
		return
	}

	// Compute center frequency (probability of value 0)
	laplaceScale := laplaceFS - laplaceNMin
	fs0 := laplaceNMin + (laplaceScale*decay)>>15
	if fs0 > laplaceFS-1 {
		fs0 = laplaceFS - 1
	}

	if val == 0 {
		re.Encode(0, uint32(fs0), uint32(laplaceFS))
		return
	}

	// For positive values (intensity band is always >= 0)
	k := 1
	cumFL := fs0
	prevFk := fs0

	for k < val {
		fk := max((prevFk*decay)>>15, laplaceNMin)
		cumFL += fk
		prevFk = fk
		k++
	}

	fk := max((prevFk*decay)>>15, laplaceNMin)

	re.Encode(uint32(cumFL), uint32(cumFL+fk), uint32(laplaceFS))
}

// EncodeStereoParamsWithIntensity encodes stereo params with optional intensity stereo.
// intensityBand: band where intensity stereo starts (-1 to disable)
// dualStereo: true for dual stereo mode
//
// For future use when intensity stereo is implemented.
func (e *Encoder) EncodeStereoParamsWithIntensity(nbBands, intensityBand int, dualStereo bool) int {
	if e.rangeEncoder == nil {
		return -1
	}

	// Encode intensity band
	// If intensityBand < 0, encode nbBands (meaning no intensity stereo)
	encodeVal := nbBands
	if intensityBand >= 0 && intensityBand < nbBands {
		encodeVal = intensityBand
	}
	e.encodeLaplaceIntensity(encodeVal, IntensityDecay)

	// Encode dual_stereo flag
	var dualFlag int
	if dualStereo {
		dualFlag = 1
	}
	e.rangeEncoder.EncodeBit(dualFlag, 1)

	if intensityBand >= 0 && intensityBand < nbBands {
		return intensityBand
	}
	return -1
}

// ConvertToMidSide converts L/R stereo to mid/side representation.
// This is the inverse of MidSideToLR.
//
// The conversion is:
//
//	mid[i] = (left[i] + right[i]) / sqrt(2)
//	side[i] = (left[i] - right[i]) / sqrt(2)
//
// The sqrt(2) normalization preserves energy: |L|^2 + |R|^2 = |M|^2 + |S|^2
//
// Parameters:
//   - left: left channel samples
//   - right: right channel samples
//
// Returns: mid and side channel arrays
//
// Reference: RFC 6716 Section 4.3.4
func ConvertToMidSide(left, right []celtNorm) (mid, side []celtNorm) {
	n := len(left)
	if n == 0 {
		return nil, nil
	}

	// Handle mismatched lengths
	if len(right) < n {
		n = len(right)
	}
	if len(right) > len(left) {
		n = len(left)
	}

	mid = make([]celtNorm, n)
	side = make([]celtNorm, n)

	// sqrt(2) for energy preservation
	const invSqrt2 = float32(0.7071067811865476)

	for i := 0; i < n; i++ {
		mid[i] = celtNorm((float32(left[i]) + float32(right[i])) * invSqrt2)
		side[i] = celtNorm((float32(left[i]) - float32(right[i])) * invSqrt2)
	}

	return mid, side
}

// ConvertMidSideToLR converts mid/side to L/R representation.
// This is the inverse of ConvertToMidSide.
//
// The conversion is:
//
//	left[i] = (mid[i] + side[i]) / sqrt(2)
//	right[i] = (mid[i] - side[i]) / sqrt(2)
//
// Combined with ConvertToMidSide, this forms an identity transform:
// L,R -> M,S -> L,R (with floating point precision)
func ConvertMidSideToLR(mid, side []celtNorm) (left, right []celtNorm) {
	n := len(mid)
	if n == 0 {
		return nil, nil
	}

	if len(side) < n {
		n = len(side)
	}

	left = make([]celtNorm, n)
	right = make([]celtNorm, n)

	// Using sqrt(2)/2 = 1/sqrt(2) for reconstruction
	const invSqrt2 = float32(0.7071067811865476)

	for i := 0; i < n; i++ {
		// Inverse of the forward transform
		// M = (L+R)/sqrt(2), S = (L-R)/sqrt(2)
		// L = (M+S)/sqrt(2), R = (M-S)/sqrt(2)
		// But we need L = (M+S)*sqrt(2)/2 = (M+S)/sqrt(2) ... wait
		// Actually: M*sqrt(2) = L+R, S*sqrt(2) = L-R
		// So: L = (M+S)*sqrt(2)/2 = (M+S)/sqrt(2) ... hmm
		// Let me reconsider: if M = (L+R)/sqrt(2), S = (L-R)/sqrt(2)
		// then L+R = M*sqrt(2), L-R = S*sqrt(2)
		// 2L = (M+S)*sqrt(2), L = (M+S)*sqrt(2)/2 = (M+S)/sqrt(2)
		// Same for R: R = (M-S)/sqrt(2)
		left[i] = celtNorm((float32(mid[i]) + float32(side[i])) * invSqrt2)
		right[i] = celtNorm((float32(mid[i]) - float32(side[i])) * invSqrt2)
	}

	return left, right
}

// deinterleaveStereoScratchF32 separates interleaved float-build stereo using
// float-width scratch buffers.
func deinterleaveStereoScratchF32(interleaved []float32, leftBuf, rightBuf *[]float32) (left, right []float32) {
	if len(interleaved) < 2 {
		return nil, nil
	}

	n := len(interleaved) / 2
	left = ensureFloat32Slice(leftBuf, n)
	right = ensureFloat32Slice(rightBuf, n)
	DeinterleaveStereoIntoF32(interleaved, left, right)
	return left, right
}

// DeinterleaveStereoInto separates interleaved stereo samples into pre-allocated L and R slices.
// left and right must each have capacity >= len(interleaved)/2.
func DeinterleaveStereoInto(interleaved, left, right []celtNorm) {
	n := len(interleaved) / 2
	if n <= 0 {
		return
	}
	// BCE hints: prove to the compiler that all accesses are in-bounds.
	_ = interleaved[2*n-1]
	_ = left[n-1]
	_ = right[n-1]
	for i := range n {
		left[i] = interleaved[i*2]
		right[i] = interleaved[i*2+1]
	}
}

// DeinterleaveStereoIntoF32 separates interleaved float-build stereo samples
// into pre-allocated L and R slices.
func DeinterleaveStereoIntoF32(interleaved, left, right []float32) {
	n := len(interleaved) / 2
	if n <= 0 {
		return
	}
	_ = interleaved[2*n-1]
	_ = left[n-1]
	_ = right[n-1]
	for i := range n {
		left[i] = interleaved[i*2]
		right[i] = interleaved[i*2+1]
	}
}

// InterleaveStereoInto combines separate L and R arrays into a pre-allocated interleaved slice.
// interleaved must have capacity >= 2*min(len(left), len(right)).
func InterleaveStereoInto(left, right, interleaved []celtNorm) {
	n := len(left)
	if len(right) < n {
		n = len(right)
	}
	if len(interleaved) < n*2 || n <= 0 {
		return
	}
	_ = left[n-1]
	_ = right[n-1]
	_ = interleaved[2*n-1]
	for i := 0; i < n; i++ {
		interleaved[2*i] = left[i]
		interleaved[2*i+1] = right[i]
	}
}

// InterleaveStereoF32 combines separate float-build L and R arrays into
// interleaved format.
func InterleaveStereoF32(left, right []float32) []float32 {
	n := len(left)
	if len(right) < n {
		n = len(right)
	}
	if n == 0 {
		return nil
	}

	interleaved := make([]float32, n*2)
	for i := 0; i < n; i++ {
		interleaved[2*i] = left[i]
		interleaved[2*i+1] = right[i]
	}
	return interleaved
}

// InterleaveStereoIntoF32 combines separate float-build L and R arrays into a
// pre-allocated interleaved slice.
func InterleaveStereoIntoF32(left, right, interleaved []float32) {
	n := len(left)
	if len(right) < n {
		n = len(right)
	}
	if len(interleaved) < n*2 || n <= 0 {
		return
	}
	_ = left[n-1]
	_ = right[n-1]
	_ = interleaved[2*n-1]
	for i := 0; i < n; i++ {
		interleaved[2*i] = left[i]
		interleaved[2*i+1] = right[i]
	}
}

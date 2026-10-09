// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides stereo mode encoding for the CELT encoder.

package celt

// IntensityDecay is the decay parameter for intensity stereo Laplace encoding.
// The decoder uses the same decay value when it reads the intensity band.
const IntensityDecay = 16384

// EncodeStereoParams encodes a no-intensity stereo parameter set with dual
// stereo enabled. It writes nbBands as the intensity band, then writes a
// dual-stereo flag of 1. It returns -1 to indicate that intensity stereo is
// disabled. If no range encoder is active, it returns -1 without writing.
//
// DecodeStereoParams reads the intensity band before the dual-stereo flag.
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
// val is the intensity band; nbBands disables intensity stereo.
func (e *Encoder) encodeLaplaceIntensity(val int, decay int) {
	re := e.rangeEncoder
	if re == nil {
		return
	}

	// Compute center frequency (probability of value 0)
	laplaceScale := laplaceFS - laplaceNMin
	fs0 := min(laplaceNMin+(laplaceScale*decay)>>15, laplaceFS-1)

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

// EncodeStereoParamsWithIntensity encodes stereo parameters with optional
// intensity stereo. intensityBand selects the first intensity-stereo band when
// it is in [0, nbBands); any other value disables intensity stereo. dualStereo
// selects independent channel coding below the intensity band, or for all bands
// when intensity stereo is disabled. The return value is intensityBand when
// enabled, or -1 when disabled. If no range encoder is active, it returns -1
// without writing.
func (e *Encoder) EncodeStereoParamsWithIntensity(nbBands, intensityBand int, dualStereo bool) int {
	if e.rangeEncoder == nil {
		return -1
	}

	// Encode intensity band
	// If intensityBand is outside [0, nbBands), encode nbBands (no intensity stereo).
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
//
// The conversion is:
//
//	mid[i] = (left[i] + right[i]) / sqrt(2)
//	side[i] = (left[i] - right[i]) / sqrt(2)
//
// The normalization preserves energy: |L|^2 + |R|^2 = |M|^2 + |S|^2. The
// function processes the common prefix of left and right.
//
// Parameters:
//   - left: left channel samples
//   - right: right channel samples
//
// Returns: mid and side channel arrays
//
// Reference: RFC 6716 Section 4.3.4
func ConvertToMidSide(left, right []celtNorm) (mid, side []CeltNorm) {
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
// The function processes the common prefix of mid and side.
func ConvertMidSideToLR(mid, side []celtNorm) (left, right []CeltNorm) {
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
		// Apply the inverse orthonormal mid-side transform.
		left[i] = celtNorm((float32(mid[i]) + float32(side[i])) * invSqrt2)
		right[i] = celtNorm((float32(mid[i]) - float32(side[i])) * invSqrt2)
	}

	return left, right
}

// DeinterleaveStereoInto separates interleaved stereo samples into L and R
// slices. It processes len(interleaved)/2 samples per channel, ignoring a
// trailing unpaired sample. left and right must each have at least that length.
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

// DeinterleaveStereoIntoF32 separates interleaved float32 stereo samples into
// L and R slices. It processes len(interleaved)/2 samples per channel, ignoring
// a trailing unpaired sample. left and right must each have at least that length.
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

// InterleaveStereoInto combines the common prefix of separate L and R slices
// into interleaved stereo. interleaved must have length at least twice the
// common-prefix length. Extra destination elements remain unchanged; if the
// destination is too short or either source is empty, the function returns
// without writing.
func InterleaveStereoInto(left, right, interleaved []celtNorm) {
	n := min(len(right), len(left))
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
	n := min(len(right), len(left))
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
	n := min(len(right), len(left))
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

package celt

// MDCTForwardScratch holds the CELT float MDCT working buffers for callers
// that perform repeated transforms outside an Encoder.
type MDCTForwardScratch struct {
	f      []float32
	fftIn  []complex64
	fftOut []complex64
	fftTmp []kissCpx
}

// ForwardWithOverlapFloat32Into writes the same coefficients as
// MDCTForwardWithOverlapFloat32 into caller-owned storage. Once the scratch
// has reached the required frame size, this path does not allocate.
func (s *MDCTForwardScratch) ForwardWithOverlapFloat32Into(samples []float32, overlap int, coeffs []float32) {
	frameSize := len(samples) - overlap
	if overlap < 0 || frameSize <= 0 || len(coeffs) < frameSize {
		return
	}
	n4 := frameSize / 2
	if n4 <= 0 {
		return
	}
	f := ensureFloat32Slice(&s.f, frameSize)
	fftIn := ensureComplex64Slice(&s.fftIn, n4)
	fftOut := ensureComplex64Slice(&s.fftOut, n4)
	fftTmp := ensureKissCpxSlice(&s.fftTmp, n4)
	mdctForwardOverlapF32Scratch(samples, overlap, coeffs[:frameSize], f, fftIn, fftOut, fftTmp, nil)
}

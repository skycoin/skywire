package celt

import (
	"math"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// IMDCT (Inverse Modified Discrete Cosine Transform) implementation for CELT.
// This file provides FFT-based IMDCT for efficient frequency-to-time conversion.
//
// The IMDCT is the core synthesis transform in CELT, converting frequency-domain
// MDCT coefficients back to time-domain audio samples. Using FFT reduces complexity
// from O(n^2) to O(n log n).
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/mdct.c

// mdctTransformLookup stores one block size from a mode's clt_mdct_init table.
type mdctTransformLookup struct {
	n      int
	trig   []float32
	window []float32
	fft    *kissFFTState
}

func buildMDCTTrigF32(n int) []float32 {
	if n <= 0 {
		return nil
	}
	n2 := n / 2
	trig := make([]float32, n2)
	for i := range n2 {
		// libopus celt/mdct.c clt_mdct_init float build:
		//   trig[i] = (float)cos(2*PI*(i+.125)/N)
		// computed in double precision and rounded to float. The float32
		// polynomial cosine does not match libm here, which matters for the
		// non-standard MDCT lengths of the Opus Custom Fs==400*shortMdctSize
		// family (e.g. N=120 short blocks at 24000/480). The standard 48 kHz
		// lengths take precomputed tables in getMDCTTrigF32 and are unaffected.
		angle := 2.0 * math.Pi * (float64(i) + 0.125) / float64(n)
		trig[i] = float32(math.Cos(angle))
	}
	return trig
}

func getMDCTTrigF32(n int) []float32 {
	switch n {
	case 240:
		return mdctTrig240F32Static[:]
	case 480:
		return mdctTrig480F32Static[:]
	case 960:
		// Use exact libopus twiddle segment for 48kHz 10ms long-block MDCT.
		return mdctTrig960F32Static[:]
	case 1920:
		return mdctTrig1920F32Static[:]
	case 3840:
		return getMDCTTrig3840F32()
	default:
		return buildMDCTTrigF32(n)
	}
}

// IMDCT computes the inverse MDCT of frequency coefficients.
// Input: n frequency bins (spectrum)
// Output: 2*n time samples
//
// For power-of-2 sizes, uses FFT-based approach for O(n log n) complexity.
// For other sizes (like CELT's 120, 240, 480, 960), uses direct computation
// which is O(n^2) but handles any size correctly.
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/mdct.c
func IMDCT(spectrum []float32) []float32 {
	return IMDCTDirect(spectrum)
}

// IMDCTOverlapWithPrev computes CELT IMDCT using the provided overlap history.
// The returned slice includes frameSize+overlap samples.
func IMDCTOverlapWithPrev(spectrum, prevOverlap []float32, overlap int) []float32 {
	if len(spectrum) == 0 {
		return nil
	}
	if prevOverlap == nil {
		prevOverlap = make([]float32, overlap)
	}
	return imdctOverlapWithPrevScratchF32Output32(spectrum, prevOverlap, overlap, nil)
}

func imdctOverlapWithPrev(spectrum []float32, prevOverlap []float32, overlap int) []float32 {
	n2 := len(spectrum)
	if n2 == 0 {
		return nil
	}
	if overlap < 0 {
		overlap = 0
	}

	out := make([]float32, n2+overlap)
	imdctOverlapWithPrevScratch(out, spectrum, prevOverlap, overlap, nil)
	return out
}

func imdctOverlapWithPrevScratch(out []float32, spectrum []float32, prevOverlap []float32, overlap int, scratch *imdctScratch) {
	n2 := len(spectrum)
	if n2 == 0 {
		return
	}
	if overlap < 0 {
		overlap = 0
	}

	needed := n2 + overlap
	if len(out) < needed {
		return
	}
	imdctOverlapWithPrevScratchF32(out, spectrum, prevOverlap, overlap, scratch)
}

func imdctPreRotateF32Spectrum(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	if n4 <= 0 {
		return
	}

	_ = spectrum[n2-1]
	_ = trig[n2-1]
	_ = fftIn[n4-1]
	imdctPreRotate(fftIn, spectrum, trig, n2, n4)
}

// imdctPreRotateNoFMAScalar is the split multiply/add libopus pre-rotation.
func imdctPreRotateNoFMAScalar(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	for i := range n4 {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		fftIn[i] = complex(
			noFMA32Sub(noFMA32Mul(x1, t0), noFMA32Mul(x2, t1)),
			noFMA32Add(noFMA32Mul(x2, t0), noFMA32Mul(x1, t1)),
		)
	}
}

func imdctOverlapWithPrevScratchF32Output32(spectrum []float32, prevOverlap []celtSig, overlap int, scratch *imdctScratchF32) []float32 {
	n2 := len(spectrum)
	if n2 == 0 {
		return nil
	}
	if overlap < 0 {
		overlap = 0
	}

	n := n2 * 2
	needed := n2 + overlap
	var tables *mdctTransformLookup
	if scratch != nil {
		tables = scratch.mdctLookup(n)
	}
	var trig []float32
	var fftState *kissFFTState
	if tables != nil {
		trig, fftState = tables.trig, tables.fft
	} else {
		trig = getMDCTTrigF32(n)
	}

	var outF32 []float32
	if scratch == nil {
		outF32 = make([]float32, needed)
	} else {
		outF32 = ensureFloat32Slice(&scratch.out, needed)
	}
	imdctOverlapWithPrevInto(outF32, spectrum, prevOverlap, overlap, scratch, tables, trig, fftState)
	return outF32[:needed:needed]
}

// imdctOverlapWithPrevInto is imdctOverlapWithPrevScratchF32Output32 writing
// its len(spectrum)+overlap samples into outF32 (with tables, trig and
// fftState already resolved for the transform size), the clt_mdct_backward_c()
// call celt_synthesis makes straight into the decode memory.
func imdctOverlapWithPrevInto(outF32, spectrum []float32, prevOverlap []celtSig, overlap int, scratch *imdctScratchF32, tables *mdctTransformLookup, trig []float32, fftState *kissFFTState) {
	n2 := len(spectrum)
	n4 := n2 / 2
	needed := n2 + overlap
	start := overlap / 2
	var fftIn []complex64
	var fftTmp []kissCpx
	if scratch == nil {
		fftIn = make([]complex64, n4)
		fftTmp = make([]kissCpx, n4)
	} else {
		fftIn = ensureComplex64Slice(&scratch.fftIn, n4)
		fftTmp = ensureKissCpxSlice(&scratch.fftTmp, n4)
	}

	if start+n2 < needed {
		clear(outF32[start+n2 : needed])
	}
	if overlap > 0 && len(prevOverlap) > 0 {
		copyLen := copy(outF32[:overlap], prevOverlap)
		if copyLen < overlap {
			clear(outF32[copyLen:overlap])
		}
	} else if overlap > 0 {
		clear(outF32[:overlap])
	}

	buf := outF32[start : start+n2]
	fftOut := imdctPreRotateFFT(fftIn, fftTmp, spectrum, trig, n2, n4, fftState)
	imdctPostRotateF32FromKiss(buf, fftOut, trig, n2, n4)

	if overlap > 0 {
		var windowF32 []float32
		if tables != nil {
			windowF32 = tables.window
		} else {
			windowF32 = GetWindowBufferF32(overlap)
		}
		imdctTDACWindow(outF32, outF32, windowF32, 0, overlap-1, overlap-1, overlap-1, overlap/2)
	}
}

// gatherStrided sets dst[i] = src[first+i*stride], the interleaved short
// block that celt_synthesis hands clt_mdct_backward_c() with stride B.
func gatherStrided(dst, src []float32, first, stride int) {
	if len(dst) == 0 {
		return
	}
	src = src[first : first+(len(dst)-1)*stride+1]
	idx := 0
	for i := range dst {
		dst[i] = src[idx]
		idx += stride
	}
}

// imdctShortBlockInto runs the clt_mdct_backward_c() call celt_synthesis
// makes for short block b of a transient frame: it reads the block's n2
// coefficients coeffs[b], coeffs[b+shortBlocks], ..., post-rotates straight
// into out[blockStart+overlap/2 : blockStart+overlap/2+n2], and applies the
// TDAC window in place against the overlap the previous block left in
// out[blockStart : blockStart+overlap/2]. gather backs the contiguous copy a
// lane without a strided pre-rotation needs.
func imdctShortBlockInto(coeffs []float32, b, shortBlocks, n2 int, out []float32, blockStart, overlap int, scratch *imdctScratchF32, gather []float32) {
	n := n2 * 2
	n4 := n2 / 2
	tables := scratch.mdctLookup(n)
	var trig []float32
	var fftState *kissFFTState
	if tables != nil {
		trig, fftState = tables.trig, tables.fft
	} else {
		trig = getMDCTTrigF32(n)
	}
	fftIn := ensureComplex64Slice(&scratch.fftIn, n4)
	fftTmp := ensureKissCpxSlice(&scratch.fftTmp, n4)
	fftOut := imdctPreRotateFFTStrided(fftIn, fftTmp, coeffs, b, shortBlocks, trig, n2, n4, fftState, gather)
	start := blockStart + overlap/2
	imdctPostRotateF32FromKiss(out[start:start+n2], fftOut, trig, n2, n4)
	if overlap > 0 {
		var windowF32 []float32
		if tables != nil {
			windowF32 = tables.window
		} else {
			windowF32 = GetWindowBufferF32(overlap)
		}
		xp1 := blockStart + overlap - 1
		imdctTDACWindow(out, out, windowF32, blockStart, xp1, xp1, overlap-1, overlap/2)
	}
}

func imdctInPlaceScratchF32Spectrum(spectrum []float32, out []float32, blockStart, overlap int, scratch *imdctScratchF32) {
	n2 := len(spectrum)
	if n2 == 0 {
		return
	}
	if overlap < 0 {
		overlap = 0
	}

	n := n2 * 2
	n4 := n2 / 2
	var tables *mdctTransformLookup
	if scratch != nil {
		tables = scratch.mdctLookup(n)
	}
	var trig []float32
	var fftState *kissFFTState
	if tables != nil {
		trig, fftState = tables.trig, tables.fft
	} else {
		trig = getMDCTTrigF32(n)
	}

	var fftIn []complex64
	var buf []float32
	var fftTmp []kissCpx
	if scratch == nil {
		fftIn = make([]complex64, n4)
		fftTmp = make([]kissCpx, n4)
		buf = make([]float32, n2)
	} else {
		fftIn = ensureComplex64Slice(&scratch.fftIn, n4)
		fftTmp = ensureKissCpxSlice(&scratch.fftTmp, n4)
		buf = ensureFloat32Slice(&scratch.buf, n2)
	}

	fftOut := imdctPreRotateFFT(fftIn, fftTmp, spectrum, trig, n2, n4, fftState)
	imdctPostRotateF32FromKiss(buf, fftOut, trig, n2, n4)

	start := blockStart + overlap/2
	if start >= len(out) {
		return
	}

	if overlap > 0 {
		var windowF32 []float32
		if tables != nil {
			windowF32 = tables.window
		} else {
			windowF32 = GetWindowBufferF32(overlap)
		}
		xp1 := blockStart + overlap - 1
		imdctTDACWindow(out, buf, windowF32, blockStart, xp1, xp1-start, overlap-1, overlap/2)
	}

	copyStart := 0
	if overlap > 0 {
		copyStart = overlap / 2
	}
	limit := n2
	if start+limit > len(out) {
		limit = len(out) - start
	}
	copy(out[start+copyStart:start+limit], buf[copyStart:limit])
}

// imdctOverlapWithPrevScratchF32 performs IMDCT using float32 precision to match libopus.
// This is used for long (non-transient) blocks.
func imdctOverlapWithPrevScratchF32(out []float32, spectrum []float32, prevOverlap []celtSig, overlap int, scratch *imdctScratchF32) {
	n2 := len(spectrum)
	if n2 == 0 {
		return
	}
	if overlap < 0 {
		overlap = 0
	}

	needed := n2 + overlap
	if len(out) < needed {
		return
	}

	outF32 := imdctOverlapWithPrevScratchF32Output32(spectrum, prevOverlap, overlap, scratch)
	if len(outF32) == 0 {
		return
	}
	copy(out[:needed], outF32[:needed])
}

// IMDCTShort computes IMDCT for transient frames with multiple short blocks.
// coeffs: interleaved coefficients for shortBlocks MDCTs
// shortBlocks: number of short MDCTs (2, 4, or 8)
// Returns: interleaved time samples with proper overlap handling.
//
// In transient mode, CELT uses multiple shorter MDCTs instead of one long MDCT.
// This provides better time resolution for transients (like drum hits) at the
// cost of reduced frequency resolution.
//
// Reference: libopus celt/celt_decoder.c, transient mode handling
func IMDCTShort(coeffs []float32, shortBlocks int) []float32 {
	if shortBlocks <= 1 {
		return IMDCT(coeffs)
	}

	totalCoeffs := len(coeffs)
	if totalCoeffs == 0 {
		return nil
	}

	// Each short block has totalCoeffs/shortBlocks coefficients
	shortSize := totalCoeffs / shortBlocks
	if shortSize <= 0 {
		return IMDCT(coeffs)
	}

	// Output: each short IMDCT produces 2*shortSize samples
	// With overlap, total output is shortSize*(shortBlocks+1)
	// But for simplicity, we produce 2*totalCoeffs and let caller handle overlap
	output := make([]float32, 2*totalCoeffs)

	// Process each short block
	for b := range shortBlocks {
		// Extract coefficients for this short block
		shortCoeffs := make([]float32, shortSize)
		for i := range shortSize {
			// Coefficients are interleaved: coeff[b + i*shortBlocks]
			srcIdx := b + i*shortBlocks
			if srcIdx < totalCoeffs {
				shortCoeffs[i] = coeffs[srcIdx]
			}
		}

		// Compute IMDCT for this short block
		shortOut := IMDCT(shortCoeffs)

		// Place output in interleaved fashion
		// Output position for this block
		outOffset := b * shortSize * 2
		for i := 0; i < len(shortOut) && outOffset+i < len(output); i++ {
			output[outOffset+i] = shortOut[i]
		}
	}

	return output
}

func dft32(x []complex64) []complex64 {
	n := len(x)
	if n <= 1 {
		return x
	}

	out := make([]complex64, n)
	twoPi := float32(-2.0*math.Pi) / float32(n)
	for k := range n {
		angle := twoPi * float32(k)
		wStep := complex(opusmath.CosF32(angle), opusmath.SinF32(angle))
		w := complex(float32(1.0), float32(0.0))
		var sum complex64
		for t := range n {
			sum += x[t] * w
			w *= wStep
		}
		out[k] = sum
	}
	return out
}

func mdctCosApprox32(x float32) float32 {
	const (
		pi    float32 = 3.14159265358979323846
		twoPi float32 = 6.28318530717958647692
	)

	x -= float32(int32(x/twoPi)) * twoPi
	for x > pi {
		x -= twoPi
	}
	for x < -pi {
		x += twoPi
	}

	x2 := x * x
	return 1 - x2*(0.5-x2*(0.041666667-x2*(0.0013888889-x2*0.000024801587)))
}

// IMDCTDirect computes IMDCT per RFC 6716 Section 4.3.5.
// Formula: y[n] = sum_{k=0}^{N-1} X[k] * cos(pi/N * (n + 0.5 + N/2) * (k + 0.5))
// Input: N frequency coefficients
// Output: 2*N time samples
// Normalization: matches libopus test_unit_mdct.c inverse (no extra scaling)
//
// This is O(n^2) but mathematically exact and handles non-power-of-2 sizes
// (like CELT's 120, 240, 480, 960) that the FFT-based approach cannot.
func IMDCTDirect(spectrum []float32) []float32 {
	N := len(spectrum)
	if N <= 0 {
		return nil
	}

	N2 := N * 2
	output := make([]float32, N2)
	base := float32(math.Pi) / float32(N)
	nHalf := float32(N) / 2
	for n := range N2 {
		var sum float32
		nTerm := float32(n) + 0.5 + nHalf
		for k := range N {
			angle := base * nTerm * (float32(k) + 0.5)
			sum += spectrum[k] * mdctCosApprox32(angle)
		}
		output[n] = sum
	}

	return output
}

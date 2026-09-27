package celt

import (
	"github.com/thesyncim/gopus/internal/arena"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func ensureInt32Slice(buf *[]int32, n int) []int32 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]int32, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureByteSlice(buf *[]byte, n int) []byte {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]byte, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureComplex64Slice(buf *[]complex64, n int) []complex64 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]complex64, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureFloat32Slice(buf *[]float32, n int) []float32 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]float32, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureUint32Slice(buf *[]uint32, n int) []uint32 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]uint32, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureKissCpxSlice(buf *[]kissCpx, n int) []kissCpx {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]kissCpx, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

type bandDecodeScratch struct {
	// floatScratch backs the band-decode-local float-family scratch (left, right,
	// norm, lowband, pvqNorm/foldResult, hadamardTmpNorm, quantWork) with
	// one contiguous allocation instead of nine. Carved in ensureFloatScratch at the
	// top of the band decode; the per-field ensure* sizing/getters reslice/clear
	// within their cap-pinned slot. (coeffs is excluded: it is an IMDCT-stage buffer,
	// not used here; bandVectors*/bandStorage* hold pointers and stay separate.)
	floatScratch arena.Bump[celtNorm]

	left     []celtNorm
	right    []celtNorm
	collapse []byte
	norm     []celtNorm
	lowband  []celtNorm

	// Pre-allocated buffers for DecodeBands hot path (eliminates per-frame allocations)
	coeffs       []celtNorm   // MDCT coefficients output buffer (size: frameSize or 2*frameSize for stereo)
	bandVectors  [][]celtNorm // Per-band decoded vectors for folding (size: MaxBands)
	bandVectorsL [][]celtNorm // Left channel band vectors for stereo (size: MaxBands)
	bandVectorsR [][]celtNorm // Right channel band vectors for stereo (size: MaxBands)

	// Individual band vector storage - flat storage to avoid slice-of-slice allocations
	// Each band can have up to maxBandWidth bins (scaled band 20 at 20ms = 176 bins)
	bandStorage  [MaxBands][]celtNorm // Pre-allocated storage for each band vector
	bandStorageL [MaxBands][]celtNorm // Left channel band storage
	bandStorageR [MaxBands][]celtNorm // Right channel band storage

	// Scratch buffers for PVQ/folding operations
	pvqPulses  []int32 // Pulse vector from CWRS decode; libopus uses C int.
	pvqNorm    []celtNorm
	foldResult []celtNorm
	cwrsU      []uint32 // CWRS u-row scratch buffer

	// Scratch buffers for Hadamard interleave/deinterleave (eliminates per-call allocations)
	hadamardTmpNorm []celtNorm
	quantWork       []celtNorm // Deinterleaved working buffer for quantBand decode

}

// bandEncodeScratch holds pre-allocated buffers for stereo encoding hot path.
// This eliminates per-band allocations in quantAllBandsEncode.
type bandEncodeScratch struct {
	// Main buffers for quantAllBandsEncode
	collapse       []byte
	norm           []celtNorm
	lowbandScratch []celtNorm

	// floatScratch backs the encode band float-family scratch (norm,
	// lowbandScratch, hadamardTmpNorm, pvqY/pvqAbsX/pvqX, and the eight theta-RDO
	// slots below) with one contiguous allocation so they share a cache region
	// and cost one alloc instead of ~14. Each field's ensure* getter still
	// reslices/clears its own cap-pinned slot independently, so semantics are
	// unchanged; a request wider than the carved slot (custom modes) just makes a
	// fresh slice in that slot, identical to before. See ensureFloatScratch.
	floatScratch arena.Bump[celtNorm]

	// Theta RDO buffers (for stereo encoding): eight per-band celtNorm slots,
	// each bounded by maxBandWidth, all live simultaneously within one band's RDO.
	xSave       []celtNorm
	ySave       []celtNorm
	normSave    []celtNorm
	xResult0    []celtNorm
	yResult0    []celtNorm
	normResult0 []celtNorm
	thetaX      []celtNorm
	thetaY      []celtNorm

	// Theta RDO encoder state saves (reusable across bands)
	ecSave     rangecoding.EncoderState
	ecSave0    rangecoding.EncoderState
	extEcSave  rangecoding.EncoderState
	extEcSave0 rangecoding.EncoderState

	// PVQ scratch buffers
	pvqSignx []byte
	pvqY     []float32
	pvqAbsX  []float32
	pvqX     []celtNorm
	pvqIy    []int32
	qextIy   []int32 // QEXT cubic pulse scratch; libopus uses C int.

	// CWRS scratch
	cwrsU []uint32

	// Hadamard scratch
	hadamardTmpNorm []celtNorm
	quantWork       []celtNorm
}

// Encoder scratch buffer methods

// ensureCollapse returns a pre-allocated collapse mask buffer.
func (s *bandEncodeScratch) ensureCollapse(n int) []byte {
	return ensureByteSlice(&s.collapse, n)
}

// ensureNorm returns a pre-allocated norm buffer.
func (s *bandEncodeScratch) ensureNorm(n int) []celtNorm {
	return ensureNormSlice(&s.norm, n)
}

// ensureLowbandScratch returns a pre-allocated lowband scratch buffer.
func (s *bandEncodeScratch) ensureLowbandScratch(n int) []celtNorm {
	return ensureNormSlice(&s.lowbandScratch, n)
}

// ensureFloatScratch lazily backs the band float-family scratch with one
// contiguous allocation, carving one cap-pinned slot per field. Each slot keeps
// cap == its worst-case width so the per-field ensure* getters reslice/clear
// within it exactly as they would a standalone buffer. The carved length is 0;
// callers' ensure* getters (or the per-frame ensureScratch sizing) set the
// visible length and clearing. Sizes depend only on channels (not frameSize), so
// this allocates once per encoder. Idempotent.
func (s *bandEncodeScratch) ensureFloatScratch(channels int) {
	const maxPVQN = maxBandWidth * 2
	normLen := 8 * EBands[MaxBands-1]
	maxBand := 8 * (EBands[MaxBands] - EBands[MaxBands-1])
	total := channels*normLen + maxBand + maxBandWidth*16 + 3*maxPVQN + 8*maxBandWidth
	if s.floatScratch.Cap() >= total {
		return
	}
	s.floatScratch.Ensure(total)
	s.norm = s.floatScratch.Alloc(channels * normLen)
	s.lowbandScratch = s.floatScratch.Alloc(maxBand)
	s.hadamardTmpNorm = s.floatScratch.Alloc(maxBandWidth * 16)
	s.pvqY = s.floatScratch.Alloc(maxPVQN)
	s.pvqAbsX = s.floatScratch.Alloc(maxPVQN)
	s.pvqX = s.floatScratch.Alloc(maxPVQN)
	s.xSave = s.floatScratch.Alloc(maxBandWidth)
	s.ySave = s.floatScratch.Alloc(maxBandWidth)
	s.normSave = s.floatScratch.Alloc(maxBandWidth)
	s.xResult0 = s.floatScratch.Alloc(maxBandWidth)
	s.yResult0 = s.floatScratch.Alloc(maxBandWidth)
	s.normResult0 = s.floatScratch.Alloc(maxBandWidth)
	s.thetaX = s.floatScratch.Alloc(maxBandWidth)
	s.thetaY = s.floatScratch.Alloc(maxBandWidth)
}

// ensureXSave returns a pre-allocated buffer for saving X during theta RDO.
func (s *bandEncodeScratch) ensureXSave(n int) []celtNorm {
	return ensureNormSlice(&s.xSave, n)
}

// ensureYSave returns a pre-allocated buffer for saving Y during theta RDO.
func (s *bandEncodeScratch) ensureYSave(n int) []celtNorm {
	return ensureNormSlice(&s.ySave, n)
}

// ensureNormSave returns a pre-allocated buffer for saving norm during theta RDO.
func (s *bandEncodeScratch) ensureNormSave(n int) []celtNorm {
	return ensureNormSlice(&s.normSave, n)
}

// ensureXResult0 returns a pre-allocated buffer for X result during theta RDO.
func (s *bandEncodeScratch) ensureXResult0(n int) []celtNorm {
	// Caller copy()s the full n elements in before any read, so the zero-fill is dead work.
	return ensureNormSliceNoClear(&s.xResult0, n)
}

// ensureYResult0 returns a pre-allocated buffer for Y result during theta RDO.
func (s *bandEncodeScratch) ensureYResult0(n int) []celtNorm {
	return ensureNormSliceNoClear(&s.yResult0, n)
}

// ensureNormResult0 returns a pre-allocated buffer for norm result during theta RDO.
func (s *bandEncodeScratch) ensureNormResult0(n int) []celtNorm {
	return ensureNormSlice(&s.normResult0, n)
}

func (s *bandEncodeScratch) ensureThetaX(n int) []celtNorm {
	// Callers copy() the full n elements in before any read, so the zero-fill is dead work.
	return ensureNormSliceNoClear(&s.thetaX, n)
}

func (s *bandEncodeScratch) ensureThetaY(n int) []celtNorm {
	return ensureNormSliceNoClear(&s.thetaY, n)
}

func (s *bandEncodeScratch) ensureHadamardTmpNorm(n int) []celtNorm {
	// (de)interleaveHadamardInto writes all n elements before the copy-back, so the zero-fill is dead work.
	return ensureNormSliceNoClear(&s.hadamardTmpNorm, n)
}

// ensureQuantWork returns a pre-allocated deinterleaved working buffer.
func (s *bandEncodeScratch) ensureQuantWork(n int) []celtNorm {
	return ensureNormSlice(&s.quantWork, n)
}

func (s *bandEncodeScratch) ensureQEXTIy(n int) []int32 {
	return ensureInt32Slice(&s.qextIy, n)
}

// maxBandWidth is the maximum width of any single band (band 20 at LM=3 = 176 bins).
const maxBandWidth = 176

// ensureCoeffs returns a pre-allocated coefficients buffer of the requested size.
// ensureFloatScratch backs the band-decode-local float scratch with one
// contiguous arena, carving a cap-pinned slot per field. Sizes follow the inline
// and getter requests in quantAllBandsDecodeWithScratchWithMode (left/right at
// frameSize, norm at channels*normLen, lowband at maxBand, the PVQ/fold buffers at
// maxBandWidth, hadamard/quantWork at a generous maxBandWidth*16 upper bound). The
// carved length is 0; the per-field ensure* sizing/getters set length and clearing
// within their slot. Re-carves only when a frame needs more than the current
// backing; otherwise a cheap early return. Layout only — bit-exact.
func (s *bandDecodeScratch) ensureFloatScratch(channels, frameSize, normLen, maxBand int) {
	const small = maxBandWidth
	const big = maxBandWidth * 16
	total := frameSize*2 + channels*normLen + maxBand + small*3 + big*2
	if s.floatScratch.Cap() >= total {
		return
	}
	s.floatScratch.Ensure(total)
	s.left = s.floatScratch.Alloc(frameSize)
	s.right = s.floatScratch.Alloc(frameSize)
	s.norm = s.floatScratch.Alloc(channels * normLen)
	s.lowband = s.floatScratch.Alloc(maxBand)
	s.pvqNorm = s.floatScratch.Alloc(small)
	s.foldResult = s.floatScratch.Alloc(small)
	s.hadamardTmpNorm = s.floatScratch.Alloc(big)
	s.quantWork = s.floatScratch.Alloc(big)
}

func (s *bandDecodeScratch) ensureCoeffs(n int) []celtNorm {
	return ensureNormSlice(&s.coeffs, n)
}

// ensureBandVectors returns a pre-allocated slice of band vector pointers.
// The returned slice has length nbBands, with each element pointing to
// pre-allocated storage in bandStorage.
func (s *bandDecodeScratch) ensureBandVectors(nbBands int) [][]celtNorm {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	// Ensure the slice of slices has enough capacity
	if cap(s.bandVectors) < nbBands {
		s.bandVectors = make([][]celtNorm, nbBands)
	} else {
		s.bandVectors = s.bandVectors[:nbBands]
	}
	// Clear the slice references (they will be set per-band)
	for i := 0; i < nbBands; i++ {
		s.bandVectors[i] = nil
	}
	return s.bandVectors
}

// ensureBandVectorsStereo returns pre-allocated slices for left and right channel band vectors.
func (s *bandDecodeScratch) ensureBandVectorsStereo(nbBands int) (left, right [][]celtNorm) {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	// Ensure left channel slice
	if cap(s.bandVectorsL) < nbBands {
		s.bandVectorsL = make([][]celtNorm, nbBands)
	} else {
		s.bandVectorsL = s.bandVectorsL[:nbBands]
	}
	// Ensure right channel slice
	if cap(s.bandVectorsR) < nbBands {
		s.bandVectorsR = make([][]celtNorm, nbBands)
	} else {
		s.bandVectorsR = s.bandVectorsR[:nbBands]
	}
	// Clear the slice references
	for i := 0; i < nbBands; i++ {
		s.bandVectorsL[i] = nil
		s.bandVectorsR[i] = nil
	}
	return s.bandVectorsL, s.bandVectorsR
}

// getBandStorage returns a pre-allocated buffer for storing a band vector.
// The buffer is sized to fit n elements.
func (s *bandDecodeScratch) getBandStorage(band, n int) []celtNorm {
	if band < 0 || band >= MaxBands || n <= 0 {
		return nil
	}
	return ensureNormSlice(&s.bandStorage[band], n)
}

// getBandStorageL returns a pre-allocated buffer for left channel band vector.
func (s *bandDecodeScratch) getBandStorageL(band, n int) []celtNorm {
	if band < 0 || band >= MaxBands || n <= 0 {
		return nil
	}
	return ensureNormSlice(&s.bandStorageL[band], n)
}

// getBandStorageR returns a pre-allocated buffer for right channel band vector.
func (s *bandDecodeScratch) getBandStorageR(band, n int) []celtNorm {
	if band < 0 || band >= MaxBands || n <= 0 {
		return nil
	}
	return ensureNormSlice(&s.bandStorageR[band], n)
}

// ensurePVQPulses returns a pre-allocated buffer for PVQ pulse vector.
func (s *bandDecodeScratch) ensurePVQPulses(n int) []int32 {
	return ensureInt32Slice(&s.pvqPulses, n)
}

// ensurePVQNorm returns a pre-allocated buffer for normalized vector.
func (s *bandDecodeScratch) ensurePVQNorm(n int) []celtNorm {
	return ensureNormSlice(&s.pvqNorm, n)
}

// ensureFoldResult returns a pre-allocated buffer for fold result.
func (s *bandDecodeScratch) ensureFoldResult(n int) []celtNorm {
	return ensureNormSlice(&s.foldResult, n)
}

// ensureCWRSU returns a pre-allocated buffer for CWRS u-row.
func (s *bandDecodeScratch) ensureCWRSU(n int) []uint32 {
	return ensureUint32Slice(&s.cwrsU, n)
}

func (s *bandDecodeScratch) ensureHadamardTmpNorm(n int) []celtNorm {
	return ensureNormSlice(&s.hadamardTmpNorm, n)
}

// ensureQuantWork returns a pre-allocated deinterleaved working buffer.
func (s *bandDecodeScratch) ensureQuantWork(n int) []celtNorm {
	return ensureNormSlice(&s.quantWork, n)
}

type imdctScratch = imdctScratchF32

// imdctScratchF32 holds scratch buffers for float32 IMDCT to avoid per-call allocations.
type imdctScratchF32 struct {
	fftIn  []complex64
	fftTmp []kissCpx
	buf    []float32
	out    []float32
}

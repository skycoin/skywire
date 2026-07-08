package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

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
	pvqNorm32  []celtNorm
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

	// Theta RDO buffers (for stereo encoding)
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
	return ensureNormSlice(&s.xResult0, n)
}

// ensureYResult0 returns a pre-allocated buffer for Y result during theta RDO.
func (s *bandEncodeScratch) ensureYResult0(n int) []celtNorm {
	return ensureNormSlice(&s.yResult0, n)
}

// ensureNormResult0 returns a pre-allocated buffer for norm result during theta RDO.
func (s *bandEncodeScratch) ensureNormResult0(n int) []celtNorm {
	return ensureNormSlice(&s.normResult0, n)
}

func (s *bandEncodeScratch) ensureThetaX(n int) []celtNorm {
	return ensureNormSlice(&s.thetaX, n)
}

func (s *bandEncodeScratch) ensureThetaY(n int) []celtNorm {
	return ensureNormSlice(&s.thetaY, n)
}

func (s *bandEncodeScratch) ensureHadamardTmpNorm(n int) []celtNorm {
	return ensureNormSlice(&s.hadamardTmpNorm, n)
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

func (s *bandDecodeScratch) ensurePVQNorm32(n int) []celtNorm {
	return ensureNormSlice(&s.pvqNorm32, n)
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

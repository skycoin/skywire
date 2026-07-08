//go:build gopus_fixed_point

package fixedpoint

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// celtEncodeScratch holds the reusable per-frame and per-band working buffers
// for the FIXED_POINT CELT encode path. The CELTEncoder owns one instance and
// reuses it across every frame of a (possibly multi-frame) packet, so the
// steady-state encode allocates nothing once the buffers have grown to the
// frame's dimensions. It mirrors the float celt.bandEncodeScratch pattern: each
// buffer is grown once via an ensure* helper that reslices when capacity is
// already sufficient. The buffers carry no cross-frame state; every consumer
// fully overwrites the prefix it uses before reading it.
type celtEncodeScratch struct {
	// EncodeWithEC frame buffers.
	in               []int32 // CC*(N+overlap) pre-emphasised input + overlap prefix
	freq             []int32 // CC*N post-MDCT spectrum
	bandE            []int32 // nbEBands*CC band energies
	bandLogE         []int32 // nbEBands*CC log2 band energies
	bandLogE2        []int32 // C*nbEBands second-MDCT log2 energies
	surroundDynalloc []int32 // C*nbEBands surround dynalloc offsets
	bandX            []int32 // C*N normalised bands
	energyErr        []int32 // C*nbEBands coarse-energy error
	offsets          []int   // nbEBands dynalloc boosts
	importance       []int   // nbEBands per-band importance
	spreadWeight     []int   // nbEBands spreading weights
	tfRes            []int   // nbEBands TF resolution decisions
	offsets32        []int32 // nbEBands dynalloc boosts (int32 view)
	fineQuant        []int32 // nbEBands fine-energy bit counts
	finePriority     []int32 // nbEBands fine-energy priorities
	pulses           []int   // nbEBands pulse allocation

	// MDCTForward per-sub-block buffers (computeMDCTs calls it B*CC times).
	mdctF  []int32  // N2 windowed reals
	mdctF2 []FFTCpx // N4 pre-rotated complex bins

	// run_prefilter buffers.
	prePeriodic [][]int32 // CC pitch-history rows of length N+maxPeriod
	preStorage  []int32   // backing storage for prePeriodic rows
	prefBefore  []int32   // CC pre-filter energies
	prefAfter   []int32   // CC post-filter energies

	// QuantAllBandsEncode frame buffers.
	qCollapse       []byte  // channels*nbEBands collapse masks (returned, discarded by caller)
	qNorm           []int32 // channels*normLen normalised reference
	qLowbandScratch []int32 // resynthAlloc lowband scratch
	qXSave          []int32 // theta-rdo X save
	qYSave          []int32 // theta-rdo Y save
	qXSave2         []int32 // theta-rdo X save (round 2)
	qYSave2         []int32 // theta-rdo Y save (round 2)
	qNormSave2      []int32 // theta-rdo norm save (round 2)

	// Per-band scratch shared by AlgQuant / OpPvqSearch / the Hadamard
	// (de)interleave; reused across every band of every frame.
	pvqIy       []int   // AlgQuant codeword (N+3 headroom)
	pvqY        []int32 // OpPvqSearch working codeword
	pvqSignx    []bool  // OpPvqSearch sign mask
	hadamardTmp []int32 // (de)interleaveHadamard transpose buffer

	// Analysis buffers.
	toneLPC      []int32 // toneDetect LPC coeffs (length 2)
	toneX        []int16 // toneDetect downsampled input
	transientTmp []int16 // TransientAnalysis high-pass scratch

	tfMetric []int   // TFAnalysis per-band metric
	tfTmp    []int32 // TFAnalysis working band
	tfTmp1   []int32 // TFAnalysis Haar working band
	tfPath0  []int   // TFAnalysis Viterbi path 0
	tfPath1  []int   // TFAnalysis Viterbi path 1

	daFollower  []int32 // DynallocAnalysis follower
	daNoise     []int32 // DynallocAnalysis noise floor
	daBandLogE3 []int32 // DynallocAnalysis smoothed band log energies
	daMask      []int32 // DynallocAnalysis surround mask
	daSig       []int32 // DynallocAnalysis surround signal

	qceOldIntra []int32 // QuantCoarseEnergy intra old-energy trial
	qceErrIntra []int32 // QuantCoarseEnergy intra error trial

	// QuantCoarseEnergy range-coder state saves, reused across frames.
	qceEncStart rangecoding.EncoderState
	qceEncIntra rangecoding.EncoderState

	// Bit-allocation result/working scratch (celt-owned slices, reused).
	allocScratch celt.AllocEncodeScratch

	// Stereo theta-RDO encoder snapshots, reused across bands/frames.
	rdoSnapPre rangecoding.EncoderSnapshot
	rdoSnap2   rangecoding.EncoderSnapshot

	pitchXLP4 []int16 // pitchSearch decimated input
	pitchYLP4 []int16 // pitchSearch decimated reference
	pitchXcor []int32 // pitchSearch cross-correlation
	pitchYY   []int32 // removeDoubling yy lookup
	pitchBuf  []int16 // PrefilterAnalysis downsampled pitch buffer
	pitchXX   []int16 // plcCeltAutocorr windowed-input scratch
}

func ensureInt32(buf *[]int32, n int) []int32 {
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

func ensureInt16(buf *[]int16, n int) []int16 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]int16, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureInt(buf *[]int, n int) []int {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]int, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureBool(buf *[]bool, n int) []bool {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]bool, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureByteScratch(buf *[]byte, n int) []byte {
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

func ensureFFTCpx(buf *[]FFTCpx, n int) []FFTCpx {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]FFTCpx, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureScratch lazily allocates the encoder's reusable encode scratch.
func (e *CELTEncoder) ensureScratch() *celtEncodeScratch {
	if e.scratch == nil {
		e.scratch = &celtEncodeScratch{}
	}
	return e.scratch
}

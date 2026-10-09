//go:build gopus_fixed_point && gopus_custom_modes

package fixedpoint

import (
	"math"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// CELTCustomMode supplies the geometry shared by a custom CELT encoder and
// decoder. Scaled 21-band modes use the static band, allocation and pulse-cache
// tables; other modes use their generated band and pulse tables.
type CELTCustomMode struct {
	Fs, FrameSize, ShortMdctSize, Overlap, MaxLM, EffEBands int
	EBands                                                  []int16
	LogN                                                    []int16
	AllocVectors                                            []uint8
	CacheIndex                                              []int16
	CacheBits, CacheCaps                                    []uint8
	ScaledBandFamily                                        bool
}

func customPreemph(fs int) (coef0, coef1, coef2, coef3 int16) {
	// celt/modes.c opus_custom_mode_create(): QCONST16 values in the
	// FIXED_POINT branch. coef2 uses SIG_SHIFT=12, coef3 uses Q13.
	switch {
	case fs < 12000:
		return 11469, -5898, 1114, 30118
	case fs < 24000:
		return 19661, -5898, 1812, 18513
	case fs < 40000:
		return 25559, -3277, 3072, 10923
	default:
		return staticMDCT48000Preemph0, 0, 4096, 8192
	}
}

func customWindowQ15(overlap int) []int16 {
	// celt/modes.c opus_custom_mode_create() FIXED_POINT/!ENABLE_QEXT
	// computes this window with double sin() and floor(.5 + 32768*x).
	window := make([]int16, overlap)
	for i := range window {
		phase := .5 * math.Pi * (float64(i) + .5) / float64(overlap)
		s := math.Sin(phase)
		w := math.Floor(.5 + 32768*math.Sin(.5*math.Pi*s*s))
		if w > 32767 {
			w = 32767
		}
		window[i] = int16(w)
	}
	return window
}

// NewCELTEncoderCustom builds fixed CELT state for a generated custom mode. A
// nil result means the MDCT size is not factorable by libopus's radix-2/3/5 FFT
// or the mode's band tables exceed the fixed custom-table bounds.
func NewCELTEncoderCustom(channels int, mode CELTCustomMode) *CELTEncoder {
	mdct := NewMDCTLookup(2*mode.FrameSize, mode.MaxLM)
	if mdct == nil {
		return nil
	}
	e := NewCELTEncoder(channels)
	e.modeFs = mode.Fs
	e.shortMdctSize = mode.ShortMdctSize
	e.overlap = mode.Overlap
	e.maxLM = mode.MaxLM
	e.effEBands = mode.EffEBands
	e.end = mode.EffEBands
	e.eBands = mode.EBands
	e.logN = mode.LogN
	if !mode.ScaledBandFamily {
		tables := celt.NewFixedCustomTables(len(mode.EBands)-1, mode.ShortMdctSize, mode.MaxLM,
			mode.EBands, mode.LogN, mode.AllocVectors, mode.CacheIndex, mode.CacheBits, mode.CacheCaps)
		if tables == nil {
			return nil
		}
		e.customTables = tables
	}
	e.maxPeriod = combFilterMaxPeriod
	e.qextScale = 1
	e.upsample = 1
	e.preemph0, e.preemph1, e.preemph2, _ = customPreemph(mode.Fs)
	e.mdct = mdct
	e.window = customWindowQ15(mode.Overlap)
	e.inMem = make([]int32, channels*mode.Overlap)
	bandStateLen := 2 * (len(mode.EBands) - 1)
	e.oldBandE = make([]int32, bandStateLen)
	e.oldLogE = make([]int32, bandStateLen)
	e.oldLogE2 = make([]int32, bandStateLen)
	e.energyError = make([]int32, bandStateLen)
	for i := range e.oldLogE {
		e.oldLogE[i], e.oldLogE2[i] = -gconst(28), -gconst(28)
	}
	return e
}

// NewCELTDecoderCustom builds the decoder for the same generated mode. A nil
// result means the MDCT size or the mode's band tables are unsupported.
func NewCELTDecoderCustom(channels int, mode CELTCustomMode) *CELTDecoder {
	mdct := NewMDCTLookup(2*mode.FrameSize, mode.MaxLM)
	if mdct == nil {
		return nil
	}
	d := NewCELTDecoder(channels)
	d.shortMdctSize = mode.ShortMdctSize
	d.overlap = mode.Overlap
	d.maxLM = mode.MaxLM
	d.effEBands = mode.EffEBands
	d.end = mode.EffEBands
	d.eBands = mode.EBands
	d.customLogN = mode.LogN
	if !mode.ScaledBandFamily {
		tables := celt.NewFixedCustomTables(len(mode.EBands)-1, mode.ShortMdctSize, mode.MaxLM,
			mode.EBands, mode.LogN, mode.AllocVectors, mode.CacheIndex, mode.CacheBits, mode.CacheCaps)
		if tables == nil {
			return nil
		}
		d.customTables = tables
	}
	d.preemph0, d.preemph1, _, d.preemph3 = customPreemph(mode.Fs)
	d.mdct = mdct
	d.window = customWindowQ15(mode.Overlap)
	d.decodeMem = make([]int32, channels*(celtDecodeBufferSize+mode.Overlap))
	bandStateLen := 2 * (len(mode.EBands) - 1)
	d.oldBandE = make([]int32, bandStateLen)
	d.oldLogE = make([]int32, bandStateLen)
	d.oldLogE2 = make([]int32, bandStateLen)
	d.backgroundLogE = make([]int32, bandStateLen)
	for i := range d.oldLogE {
		d.oldLogE[i], d.oldLogE2[i] = -gconst(28), -gconst(28)
	}
	d.deemphasisScratch = make([]int32, mode.FrameSize)
	d.prefilterFoldScratch = make([]int32, mode.Overlap)
	return d
}

func (e *CELTEncoder) quantAllBandsCustom(enc *rangecoding.Encoder, C, N, LM, start, end int,
	X, Y, bandE, pulses, tfRes []int32, shortBlocks, dualStereo, totalBitsQ3, balance, codedBands int,
	seed *uint32, scratch *celtEncodeScratch) []byte {
	geometry := celtBandGeometry{
		eBands: e.eBands, logN: e.logN, nbEBands: len(e.eBands) - 1,
		effEBands: e.effEBands, customCache: e.customTables,
	}
	return quantAllBandsEncodeMode(geometry, enc, C, N, LM, start, end, X, Y, bandE,
		pulses, tfRes, shortBlocks, e.spreadDecision, dualStereo, e.intensity,
		totalBitsQ3, balance, codedBands, e.complexity, false, seed, scratch, nil)
}

func (d *CELTDecoder) quantAllBandsCustom(dec *rangecoding.Decoder, C, N, LM, start, end int,
	pulses, tfRes []int32, shortBlocks, spread, dualStereo, intensity, totalBitsQ3, balance, codedBands int,
	seed *uint32) (left, right []int32, collapse []byte) {
	return quantAllBandsDecodeMode(dec, C, N, LM, start, end, pulses, tfRes, shortBlocks,
		spread, dualStereo, intensity, totalBitsQ3, balance, codedBands, false, seed,
		d.eBands, d.customLogN, len(d.eBands)-1, false, false, false, nil, d.customTables, &d.bandScratch)
}

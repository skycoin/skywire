//go:build gopus_fixed_point

package fixedpoint

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// This file ports the FIXED_POINT celt/celt_decoder.c celt_decode_with_ec
// driver for the static 48000/960 custom mode, orchestrating the already-ported
// integer kernels (energy unquantizers, quant_all_bands, anti_collapse,
// celt_synthesis, comb post-filter, deemphasis) into a full mono/stereo
// non-PLC frame decode that is bit-exact with the reference MODE_DECODE oracle.
//
// Scope of this increment: a fresh (loss_duration==0), non-PLC, non-QEXT decode
// of the static 48000/960 mode, including the st->downsample > 1 path that emits
// 24k/16k/12k/8k output from the 48k core. PLC (data==NULL || len<=1) and DRED
// are out of scope and not driven here.

const (
	// celtDecodeBufferSize mirrors DECODE_BUFFER_SIZE == DEC_PITCH_BUF_SIZE.
	celtDecodeBufferSize = 2048
	// celtCombFilterMinPeriod mirrors COMBFILTER_MINPERIOD.
	celtCombFilterMinPeriod = 15
	// celtNbEBands / celtOverlap / celtShortMdctSize / celtMaxLM describe the
	// static 48000/960 mode.
	celtNbEBands      = 21
	celtOverlap       = 120
	celtShortMdctSize = 120
	celtMaxLM         = 3
)

// CELTDecoder is the FIXED_POINT integer CELT decoder state for the static
// 48000/960 custom mode. It owns the cross-frame decode_mem overlap buffer, the
// energy histories and the post-filter / deemphasis state, matching the reset
// region of libopus OpusCustomDecoder.
type CELTDecoder struct {
	channels int

	// downsample mirrors st->downsample (resampling_factor of the output rate):
	// 1 for 48k, 2 for 24k, 3 for 16k, 4 for 12k, 6 for 8k.
	downsample int

	start int
	end   int

	rng          uint32
	lossDuration int

	// PLC cross-frame state, mirroring the OpusCustomDecoder reset region.
	lastPitchIndex   int
	plcDuration      int
	lastFrameType    int
	skipPLC          bool
	prefilterAndFold bool
	// lpc holds channels*celtLPCOrder opus_val16 LPC coefficients carried
	// between consecutive lost frames.
	lpc []int16

	postfilterPeriod    int
	postfilterPeriodOld int
	postfilterGain      int16
	postfilterGainOld   int16
	postfilterTapset    int
	postfilterTapsetOld int

	// decodeMem holds channels*(celtDecodeBufferSize+overlap) celt_sig samples.
	decodeMem []int32
	// oldBandE/oldLogE/oldLogE2/backgroundLogE are 2*nbEBands celt_glog each.
	oldBandE       []int32
	oldLogE        []int32
	oldLogE2       []int32
	backgroundLogE []int32
	preemphMemD    []int32

	mdct   *MDCTLookup
	window []int16
	eBands []int16

	// res holds the opus_res output of the most recent decode (the value
	// libopus writes via RES2INT24(a)=(a) for the FIXED_POINT ENABLE_RES24
	// build). int16 output derives from it via Res2Int16; int24 output is the
	// value itself. Reused across frames to avoid per-frame allocation.
	res []int32
}

// resScratch returns d.res resized to n, growing the backing array as needed.
func (d *CELTDecoder) resScratch(n int) []int32 {
	if cap(d.res) < n {
		d.res = make([]int32, n)
		return d.res
	}
	d.res = d.res[:n]
	return d.res
}

// LastRes returns the opus_res output of the most recent DecodeWithEC /
// DecodeLost call. For the FIXED_POINT ENABLE_RES24 build these int32 values
// are exactly the int24 PCM samples libopus emits (RES2INT24(a) == a), and the
// int16 output is Res2Int16 of each. The slice aliases internal scratch and is
// valid until the next decode.
func (d *CELTDecoder) LastRes() []int32 {
	return d.res
}

// NewCELTDecoder allocates and resets an integer CELT decoder for the static
// 48000/960 mode with the given channel count (1 or 2), matching
// celt_decoder_init: oldLogE/oldLogE2 are seeded to -GCONST(28.f).
func NewCELTDecoder(channels int) *CELTDecoder {
	return NewCELTDecoderRate(channels, 48000)
}

// NewCELTDecoderRate allocates and resets an integer CELT decoder for the static
// 48000/960 core with output sampling rate sampleRate (48000/24000/16000/12000/
// 8000), matching celt_decoder_init: st->downsample = resampling_factor(rate).
func NewCELTDecoderRate(channels, sampleRate int) *CELTDecoder {
	d := &CELTDecoder{
		channels:   channels,
		downsample: resamplingFactor(sampleRate),
		start:      0,
		end:        celtNbEBands,
		mdct:       NewStaticMDCTLookup48000(),
		window:     staticMDCT48000Window[:],
		eBands:     staticMDCT48000EBands[:],
	}
	d.decodeMem = make([]int32, channels*(celtDecodeBufferSize+celtOverlap))
	d.oldBandE = make([]int32, 2*celtNbEBands)
	d.oldLogE = make([]int32, 2*celtNbEBands)
	d.oldLogE2 = make([]int32, 2*celtNbEBands)
	d.backgroundLogE = make([]int32, 2*celtNbEBands)
	d.preemphMemD = make([]int32, channels)
	for i := range d.oldLogE {
		d.oldLogE[i] = -gconst(28)
		d.oldLogE2[i] = -gconst(28)
	}
	return d
}

// SetBandRange sets the active band range (st->start / st->end), matching the
// CELT_SET_START_BAND_REQUEST / CELT_SET_END_BAND_REQUEST controls.
func (d *CELTDecoder) SetBandRange(start, end int) {
	d.start = start
	d.end = end
}

// SetStartBand sets only the active start band (st->start), preserving the end
// band, matching a lone CELT_SET_START_BAND_REQUEST control. The transition PLC
// decode uses this because opus_decode_frame skips CELT_SET_END_BAND when
// bandwidth==0 (the PLC case), keeping the previous frame's end band.
func (d *CELTDecoder) SetStartBand(start int) {
	d.start = start
}

// Reset clears the cross-frame decode state, matching the OPUS_RESET_STATE
// region of OpusCustomDecoder (decode_mem, energy histories, post-filter and
// loss state) while preserving the channel count, output downsample factor and
// active band range. oldLogE/oldLogE2 are reseeded to -GCONST(28.f).
func (d *CELTDecoder) Reset() {
	clear(d.decodeMem)
	clear(d.oldBandE)
	clear(d.backgroundLogE)
	clear(d.preemphMemD)
	for i := range d.oldLogE {
		d.oldLogE[i] = -gconst(28)
		d.oldLogE2[i] = -gconst(28)
	}
	d.rng = 0
	d.lossDuration = 0
	d.lastPitchIndex = 0
	d.plcDuration = 0
	d.lastFrameType = 0
	d.skipPLC = false
	d.prefilterAndFold = false
	d.lpc = nil
	d.postfilterPeriod = 0
	d.postfilterPeriodOld = 0
	d.postfilterGain = 0
	d.postfilterGainOld = 0
	d.postfilterTapset = 0
	d.postfilterTapsetOld = 0
}

// FinalRange returns the range coder state captured after the most recent
// decode (st->rng), matching the CELT_GET_FINAL_RANGE control.
func (d *CELTDecoder) FinalRange() uint32 {
	return d.rng
}

// DecodeWithEC ports celt_decode_with_ec for a fresh non-PLC frame on the static
// 48000/960 mode. data is the CELT packet, frameSize the 48k-core per-channel
// sample count (shortMdctSize<<LM). With st->downsample > 1 the emitted output is
// decimated, so it writes channels*(frameSize/downsample) interleaved int16 PCM
// into out and returns the number of per-channel output samples decoded.
func (d *CELTDecoder) DecodeWithEC(data []byte, frameSize int, out []int16) int {
	// data == NULL || len <= 1 selects the packet-loss concealment path.
	if len(data) <= 1 {
		return d.DecodeLost(frameSize, out)
	}
	dec := &rangecoding.Decoder{}
	dec.Init(data)
	outSyn, N := d.decodeReceivedFrame(dec, len(data), frameSize)

	// deemphasis(out_syn, pcm, N, CC, st->downsample, preemph, preemph_memD, accum=0).
	outSamples := N / d.downsample
	resPCM := d.resScratch(d.channels * outSamples)
	Deemphasis(outSyn, resPCM, staticMDCT48000Preemph0, d.preemphMemD, N, d.downsample, false)
	for i := range resPCM {
		out[i] = Res2Int16(resPCM[i])
	}
	return outSamples
}

// DecodeHybridAccum decodes a hybrid CELT highband frame from the caller-provided
// range decoder, which must already be positioned after the SILK portion (and any
// redundancy-flag bits) of the packet, with its storage shrunk to exclude trailing
// redundancy bytes (matching libopus opus_decode_frame). The active band range must
// have been set via SetBandRange(start, end) with start = HybridCELTStartBand (17).
//
// It mirrors the libopus opus_decode_frame hybrid path, where
// celt_decode_with_ec_dred is called with the shared ec_dec and celt_accum=1: the
// CELT highband is accumulated (ADD_RES) onto the opus_res samples already written
// by SILK. accumPCM is the interleaved opus_res lowband buffer of length
// channels*(coreFrameSize/downsample); on return it holds the combined hybrid
// opus_res output (RES2INT24(a)==a, int16 via Res2Int16). It returns the number of
// per-channel output samples decoded.
func (d *CELTDecoder) DecodeHybridAccum(dec *rangecoding.Decoder, coreFrameSize int, accumPCM []int32) int {
	dataLen := dec.StorageBits() / 8
	outSyn, N := d.decodeReceivedFrame(dec, dataLen, coreFrameSize)

	// deemphasis(out_syn, pcm, N, CC, st->downsample, preemph, preemph_memD, accum=1).
	Deemphasis(outSyn, accumPCM, staticMDCT48000Preemph0, d.preemphMemD, N, d.downsample, true)
	return N / d.downsample
}

// decodeReceivedFrame ports the body of celt_decode_with_ec_dred for a received
// (non-PLC) frame, driving the already-positioned range decoder dec through the
// energy/PVQ/synthesis pipeline and advancing all cross-frame state (decode_mem,
// energy histories, post-filter, st->rng). dataLen is the effective packet length
// in bytes (len) used for total_bits; frameSize is the 48k-core per-channel sample
// count. It returns the per-channel synthesis buffers and N; the caller applies
// deemphasis (with or without accumulation).
func (d *CELTDecoder) decodeReceivedFrame(dec *rangecoding.Decoder, dataLen, frameSize int) ([][]int32, int) {
	nbEBands := celtNbEBands
	overlap := celtOverlap
	shortMdctSize := celtShortMdctSize
	start := d.start
	end := d.end
	CC := d.channels
	C := d.channels

	LM := 0
	for LM = 0; LM <= celtMaxLM; LM++ {
		if shortMdctSize<<LM == frameSize {
			break
		}
	}
	M := 1 << LM
	N := M * shortMdctSize

	decodeMemSize := celtDecodeBufferSize + overlap
	decodeMem := make([][]int32, CC)
	outSyn := make([][]int32, CC)
	for c := 0; c < CC; c++ {
		decodeMem[c] = d.decodeMem[c*decodeMemSize : (c+1)*decodeMemSize]
		outSyn[c] = decodeMem[c][celtDecodeBufferSize-N:]
	}

	effEnd := end
	if effEnd > nbEBands {
		effEnd = nbEBands
	}

	// Two consecutive received packets are required before the pitch-based
	// PLC is allowed again (celt_decode_with_ec: loss_duration==0 => skip_plc=0).
	if d.lossDuration == 0 {
		d.skipPLC = false
	}

	if C == 1 {
		for i := 0; i < nbEBands; i++ {
			d.oldBandE[i] = max32(d.oldBandE[i], d.oldBandE[nbEBands+i])
		}
	}

	totalBits := dataLen * 8
	tell := dec.Tell()

	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = dec.DecodeBit(15) == 1
	}
	if silence {
		// Pretend we've read all the remaining bits.
		tell = totalBits
		dec.SkipToTell(totalBits)
	}

	var postfilterGain int16
	postfilterPitch := 0
	postfilterTapset := 0
	if start == 0 && tell+16 <= totalBits {
		if dec.DecodeBit(1) == 1 {
			octave := int(dec.DecodeUniform(6))
			postfilterPitch = (16 << octave) + int(dec.DecodeRawBits(uint(4+octave))) - 1
			qg := int(dec.DecodeRawBits(3))
			if dec.Tell()+2 <= totalBits {
				postfilterTapset = dec.DecodeICDF(tapsetICDF, 2)
			}
			// QCONST16(.09375f,15)*(qg+1) = 3072*(qg+1).
			postfilterGain = int16(3072 * (qg + 1))
		}
		tell = dec.Tell()
	}

	isTransient := false
	if LM > 0 && tell+3 <= totalBits {
		isTransient = dec.DecodeBit(3) == 1
		tell = dec.Tell()
	}
	shortBlocks := 0
	if isTransient {
		shortBlocks = M
	}

	intraEner := false
	if tell+3 <= totalBits {
		intraEner = dec.DecodeBit(3) == 1
	}
	// If recovering from packet loss, make the energy prediction safe to reduce
	// the risk of loud artifacts (loss_duration != 0). The loop covers both
	// channels unconditionally, matching the do{...}while(++c<2) in libopus.
	if !intraEner && d.lossDuration != 0 {
		missing := d.lossDuration >> LM
		if missing > 10 {
			missing = 10
		}
		var safety int32
		if LM == 0 {
			safety = gconst15
		} else if LM == 1 {
			safety = gconst05
		}
		for c := 0; c < 2; c++ {
			for i := start; i < end; i++ {
				idx := c*nbEBands + i
				if d.oldBandE[idx] < max32(d.oldLogE[idx], d.oldLogE2[idx]) {
					// If energy is going down already, continue the trend.
					E0 := d.oldBandE[idx]
					E1 := d.oldLogE[idx]
					E2 := d.oldLogE2[idx]
					slope := max32(E1-E0, half32(E2-E0))
					slope = min32(slope, gconst(2))
					E0 -= max32(0, int32(1+missing)*slope)
					d.oldBandE[idx] = max32(-gconst(20), E0)
				} else {
					// Otherwise take the min of the last frames.
					d.oldBandE[idx] = min32(min32(d.oldBandE[idx], d.oldLogE[idx]), d.oldLogE2[idx])
				}
				// Shorter frames have more natural fluctuations -- play it safe.
				d.oldBandE[idx] -= safety
			}
		}
	}

	UnquantCoarseEnergy(dec, d.oldBandE, start, end, nbEBands, C, LM, intraEner)

	alloc := celt.DecodeCELTAllocation(dec, totalBits, start, end, LM, C, isTransient)

	tfRes := make([]int, end)
	for i := 0; i < end; i++ {
		tfRes[i] = int(alloc.TFRes[i])
	}
	pulses := make([]int, nbEBands)
	for i := 0; i < end; i++ {
		pulses[i] = int(alloc.Pulses[i])
	}
	fineQuant := make([]int32, nbEBands)
	finePriority := make([]int32, nbEBands)
	copy(fineQuant, alloc.FineQuant)
	copy(finePriority, alloc.FinePriority)

	UnquantFineEnergy(dec, d.oldBandE, start, end, nbEBands, C, nil, fineQuant)

	// OPUS_MOVE(decode_mem[c], decode_mem[c]+N, decode_buffer_size-N+overlap).
	moveLen := celtDecodeBufferSize - N + overlap
	for c := 0; c < CC; c++ {
		copy(decodeMem[c][:moveLen], decodeMem[c][N:N+moveLen])
	}

	seed := d.rng
	totalBitsQ3 := dataLen*(8<<bitRes) - alloc.AntiCollapseRsv
	left, right, collapse := QuantAllBandsDecode(dec, C, N, LM, start, end,
		pulses, tfRes, shortBlocks, alloc.Spread, alloc.DualStereo, alloc.Intensity,
		totalBitsQ3, alloc.Balance, alloc.CodedBands, false, &seed)

	// X is interleaved [channel0 N][channel1 N].
	X := make([]int32, C*N)
	copy(X[:N], left)
	if C == 2 {
		copy(X[N:], right)
	}

	antiCollapseOn := false
	if alloc.AntiCollapseRsv > 0 {
		antiCollapseOn = dec.DecodeRawBits(1) == 1
	}

	UnquantEnergyFinalise(dec, d.oldBandE, start, end, nbEBands, C, fineQuant, finePriority, totalBits-dec.Tell())

	if antiCollapseOn {
		// libopus passes &st->rng to quant_all_bands, which advances it (the shape
		// decode's spreading seed), then anti_collapse uses that updated st->rng as
		// its pseudo-random seed (celt_decoder.c). seed holds that advanced value;
		// using the pre-decode d.rng instead would desync the anti-collapse noise
		// fill from the reference on collapsed transient bands.
		AntiCollapse(X, collapse, LM, C, N, start, end,
			d.oldBandE, d.oldLogE, d.oldLogE2, pulses, d.eBands, nbEBands, seed, false)
	}

	if silence {
		for i := 0; i < C*nbEBands; i++ {
			d.oldBandE[i] = -gconst(28)
		}
	}

	// After a periodic-PLC frame the overlap must be pre-filtered and folded
	// (TDAC) so it blends with this frame's MDCT before synthesis.
	if d.prefilterAndFold {
		d.prefilterAndFoldImpl(N)
	}

	CeltSynthesis(d.mdct, d.window, d.eBands,
		nbEBands, shortMdctSize, celtMaxLM, overlap,
		X, outSyn, d.oldBandE,
		start, effEnd, C, CC, LM, d.downsample, isTransient, silence)

	for c := 0; c < CC; c++ {
		pp := d.postfilterPeriod
		if pp < celtCombFilterMinPeriod {
			pp = celtCombFilterMinPeriod
		}
		ppOld := d.postfilterPeriodOld
		if ppOld < celtCombFilterMinPeriod {
			ppOld = celtCombFilterMinPeriod
		}
		d.postfilterPeriod = pp
		d.postfilterPeriodOld = ppOld
		// out_syn[c] aliases decodeMem[c] at celtDecodeBufferSize-N; the comb
		// filter reads history before that offset, so operate on decodeMem[c]
		// with base = celtDecodeBufferSize-N.
		base := celtDecodeBufferSize - N
		CombFilter(decodeMem[c], decodeMem[c], base, ppOld, pp, shortMdctSize,
			d.postfilterGainOld, d.postfilterGain, d.postfilterTapsetOld, d.postfilterTapset,
			d.window, overlap)
		if LM != 0 {
			CombFilter(decodeMem[c], decodeMem[c], base+shortMdctSize, pp, postfilterPitch, N-shortMdctSize,
				d.postfilterGain, postfilterGain, d.postfilterTapset, postfilterTapset,
				d.window, overlap)
		}
	}
	d.postfilterPeriodOld = d.postfilterPeriod
	d.postfilterGainOld = d.postfilterGain
	d.postfilterTapsetOld = d.postfilterTapset
	d.postfilterPeriod = postfilterPitch
	d.postfilterGain = postfilterGain
	d.postfilterTapset = postfilterTapset
	if LM != 0 {
		d.postfilterPeriodOld = d.postfilterPeriod
		d.postfilterGainOld = d.postfilterGain
		d.postfilterTapsetOld = d.postfilterTapset
	}

	if C == 1 {
		copy(d.oldBandE[nbEBands:2*nbEBands], d.oldBandE[:nbEBands])
	}

	if !isTransient {
		copy(d.oldLogE2, d.oldLogE[:2*nbEBands])
		copy(d.oldLogE, d.oldBandE[:2*nbEBands])
	} else {
		for i := 0; i < 2*nbEBands; i++ {
			d.oldLogE[i] = min32(d.oldLogE[i], d.oldBandE[i])
		}
	}
	// max_background_increase = IMIN(160, loss_duration+M)*GCONST(0.001f).
	mbi := d.lossDuration + M
	if mbi > 160 {
		mbi = 160
	}
	maxBackgroundIncrease := int32(mbi) * gconst001
	for i := 0; i < 2*nbEBands; i++ {
		d.backgroundLogE[i] = min32(d.backgroundLogE[i]+maxBackgroundIncrease, d.oldBandE[i])
	}
	for c := 0; c < 2; c++ {
		for i := 0; i < start; i++ {
			d.oldBandE[c*nbEBands+i] = 0
			d.oldLogE[c*nbEBands+i] = -gconst(28)
			d.oldLogE2[c*nbEBands+i] = -gconst(28)
		}
		for i := end; i < nbEBands; i++ {
			d.oldBandE[c*nbEBands+i] = 0
			d.oldLogE[c*nbEBands+i] = -gconst(28)
			d.oldLogE2[c*nbEBands+i] = -gconst(28)
		}
	}
	d.rng = dec.Range()

	d.lossDuration = 0
	d.plcDuration = 0
	d.lastFrameType = frameNormal
	d.prefilterAndFold = false
	return outSyn, N
}

// resamplingFactor mirrors celt/celt.c resampling_factor for the non-QEXT,
// non-CUSTOM_MODES build: the integer decimation factor from the 48k core to the
// requested output rate. Unknown rates fall back to 1 (48k passthrough).
func resamplingFactor(rate int) int {
	switch rate {
	case 48000:
		return 1
	case 24000:
		return 2
	case 16000:
		return 3
	case 12000:
		return 4
	case 8000:
		return 6
	default:
		return 1
	}
}

// gconst001 is GCONST(0.001f) = round(0.5 + 0.001*(1<<DB_SHIFT)).
const gconst001 = int32(16777)

// tapsetICDF mirrors celt/celt.c tapset_icdf, the inverse CDF for the post-filter
// tapset symbol decoded with ec_dec_icdf(dec, tapset_icdf, 2).
var tapsetICDF = []uint8{2, 1, 0}

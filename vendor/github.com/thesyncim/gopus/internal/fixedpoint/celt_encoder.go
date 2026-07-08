//go:build gopus_fixed_point

package fixedpoint

// This file ports the FIXED_POINT (ENABLE_RES24) celt/celt_encoder.c encode
// front-end for the static 48000/960 custom mode: forward pre-emphasis
// (celt_preemphasis), the windowed forward MDCT striping (compute_mdcts) for
// the normal and transient block layouts, then compute_band_energies and
// normalise_bands, producing the interleaved post-MDCT signal (freq), the
// per-band energies (bandE) and the normalised bands (X) exactly as
// celt_encode_with_ec computes them just before quant_all_bands.
//
// Scope of this increment: the input -> normalized bands front-end only. The
// transient/prefilter/allocation/quantisation stages are out of scope here.

// int16ToRes implements libopus INT16TORES(a) for the ENABLE_RES24 build:
// SHL32(EXTEND32(a), RES_SHIFT) == a << 8.
func int16ToRes(a int16) int32 {
	return int32(a) << resShift
}

// res2sig implements libopus RES2SIG(a) for the ENABLE_RES24 build:
// SHL32(a, SIG_SHIFT-RES_SHIFT) == a << 4.
func res2sig(a int32) int32 {
	return shl32(a, sigShift-resShift)
}

// CELTEncoder is the FIXED_POINT integer CELT encoder front-end state for the
// static 48000/960 custom mode. It owns the per-channel pre-emphasis memory and
// the MDCT lookup / window / band tables, mirroring the reset region of the
// libopus OpusCustomEncoder fields the front-end touches.
type CELTEncoder struct {
	channels int

	// upsample mirrors st->upsample = resampling_factor(API sample rate): 1 at
	// 48 kHz, 2/3/4/6 at 24/16/12/8 kHz. celt_encode_with_ec multiplies the
	// passed frame_size by upsample and celt_preemphasis zero-stuffs the input to
	// the 48 kHz core rate.
	upsample int

	start int
	end   int

	// complexity / lsbDepth mirror st->complexity / st->lsb_depth.
	complexity int
	lsbDepth   int

	// bitrate is st->bitrate (bits/s), OPUS_BITRATE_MAX when unset.
	bitrate int

	// vbr / constrainedVBR mirror st->vbr and st->constrained_vbr. constrainedVBR
	// defaults to 1 (the celt_encoder_init default).
	vbr            bool
	constrainedVBR bool

	// lfe mirrors st->lfe: the low-frequency-effects encode path (forces the
	// energy clamp above band 0, disables transient/pitch/TF/surround analysis
	// and pins dynalloc/trim/bandwidth to the first band).
	lfe bool

	// energyMask mirrors st->energy_mask: when non-nil it drives the surround
	// masking / energy_mask dynalloc and trim adjustments. It holds C*nbEBands
	// celt_glog values (channel-major, Q24).
	energyMask []int32

	// VBR rate-control reservoir state (celt_encoder.c): st->vbr_reservoir,
	// st->vbr_drift, st->vbr_offset, st->vbr_count.
	vbrReservoir int32
	vbrDrift     int32
	vbrOffset    int32
	vbrCount     int32

	// preemphMemE mirrors st->preemph_memE: the per-channel pre-emphasis filter
	// state carried between frames.
	preemphMemE []int32

	// inMem holds CC*overlap celt_sig: the run_prefilter "in_mem" carried between
	// frames (the previous frame's trailing overlap, after prefiltering).
	inMem []int32
	// prefilterMem holds CC*COMBFILTER_MAXPERIOD celt_sig of pitch history.
	prefilterMem []int32

	// Cross-frame energy histories (channel-major, Q24), 2*nbEBands each so the
	// CC==2,C==1 mirroring and start/end clears have room.
	oldBandE    []int32
	oldLogE     []int32
	oldLogE2    []int32
	energyError []int32

	// Decision state carried between frames.
	prefilterPeriod int
	prefilterGain   int16
	prefilterTapset int
	consecTransient int
	delayedIntra    int32
	specAvg         int32
	intensity       int
	lastCodedBands  int
	stereoSaving    int16
	spreading       SpreadingState
	spreadDecision  int
	overlapMax      int32
	rng             uint32

	mdct   *MDCTLookup
	window []int16
	eBands []int16
	logN   []int16

	// scratch holds the reusable per-frame/per-band encode working buffers,
	// grown once and reused across every frame of a packet.
	scratch *celtEncodeScratch
}

// NewCELTEncoder allocates and resets an integer CELT encoder front-end for the
// static 48000/960 mode with the given channel count (1 or 2). All cross-frame
// state (pre-emphasis memory) starts at zero, matching celt_encoder_init.
func NewCELTEncoder(channels int) *CELTEncoder {
	return NewCELTEncoderRate(channels, 48000)
}

// NewCELTEncoderRate allocates and resets an integer CELT encoder front-end for
// the static 48000/960 mode at the given API sample rate (48000/24000/16000/
// 12000/8000), mirroring celt_encoder_init: the mode is always 48 kHz and only
// st->upsample = resampling_factor(rate) differs. The caller passes API-rate
// frame sizes (frameSize*upsample == the 48 kHz core N).
func NewCELTEncoderRate(channels, sampleRate int) *CELTEncoder {
	e := &CELTEncoder{
		channels:    channels,
		upsample:    resamplingFactor(sampleRate),
		start:       0,
		end:         celtNbEBands,
		complexity:  5,
		lsbDepth:    24,
		bitrate:     opusBitrateMax,
		preemphMemE: make([]int32, channels),
		mdct:        NewStaticMDCTLookup48000(),
		window:      staticMDCT48000Window[:],
		eBands:      staticMDCT48000EBands[:],
		logN:        staticMDCT48000LogN[:],
	}
	e.inMem = make([]int32, channels*celtOverlap)
	e.prefilterMem = make([]int32, channels*combFilterMaxPeriod)
	e.oldBandE = make([]int32, 2*celtNbEBands)
	e.oldLogE = make([]int32, 2*celtNbEBands)
	e.oldLogE2 = make([]int32, 2*celtNbEBands)
	e.energyError = make([]int32, 2*celtNbEBands)
	for i := range e.oldLogE {
		e.oldLogE[i] = -gconst(28)
		e.oldLogE2[i] = -gconst(28)
	}
	e.delayedIntra = 1
	e.spreadDecision = spreadNormal
	e.spreading = SpreadingState{TonalAverage: 256, HFAverage: 0, TapsetDecision: 0}
	e.constrainedVBR = true
	return e
}

// opusBitrateMax mirrors OPUS_BITRATE_MAX (-1).
const opusBitrateMax = -1

// SetComplexity sets st->complexity (OPUS_SET_COMPLEXITY_REQUEST).
func (e *CELTEncoder) SetComplexity(c int) { e.complexity = c }

// SetBitrate sets st->bitrate in bits/s (OPUS_SET_BITRATE_REQUEST).
func (e *CELTEncoder) SetBitrate(b int) { e.bitrate = b }

// SetVBR enables/disables variable bitrate (OPUS_SET_VBR_REQUEST).
func (e *CELTEncoder) SetVBR(v bool) { e.vbr = v }

// SetConstrainedVBR sets st->constrained_vbr (OPUS_SET_VBR_CONSTRAINT_REQUEST).
func (e *CELTEncoder) SetConstrainedVBR(v bool) { e.constrainedVBR = v }

// SetBandRange sets the active band range (st->start / st->end), matching the
// CELT_SET_START_BAND_REQUEST / CELT_SET_END_BAND_REQUEST controls.
func (e *CELTEncoder) SetBandRange(start, end int) {
	e.start = start
	e.end = end
}

// SetLFE sets st->lfe (CELT_SET_LFE_REQUEST), enabling the low-frequency-effects
// encode path.
func (e *CELTEncoder) SetLFE(v bool) { e.lfe = v }

// SetEnergyMask sets st->energy_mask (CELT_SET_ENERGY_MASK_REQUEST): a
// C*nbEBands celt_glog (Q24, channel-major) surround masking map, or nil to
// disable it.
func (e *CELTEncoder) SetEnergyMask(mask []int32) { e.energyMask = mask }

// FrontEnd ports the input -> normalized bands stage of celt_encode_with_ec for
// the static 48000/960 mode. pcm is channels*frameSize interleaved int16 PCM,
// frameSize the 48k-core per-channel sample count (shortMdctSize<<LM), and
// isTransient selects the transient MDCT striping (shortBlocks==M) over the
// normal long-block path. It returns the interleaved post-MDCT freq (celt_sig),
// the per-band bandE (celt_ener, channel-major) and the normalised X
// (celt_norm, interleaved), advancing the per-channel pre-emphasis memory.
func (e *CELTEncoder) FrontEnd(pcm []int16, frameSize int, isTransient bool) (freq, bandE, X []int32) {
	nbEBands := celtNbEBands
	overlap := celtOverlap
	shortMdctSize := celtShortMdctSize
	C := e.channels
	CC := e.channels

	LM := 0
	for LM = 0; LM <= celtMaxLM; LM++ {
		if shortMdctSize<<LM == frameSize {
			break
		}
	}
	M := 1 << LM
	N := M * shortMdctSize

	effEnd := e.end
	if effEnd > celtNbEBands {
		effEnd = celtNbEBands
	}

	shortBlocks := 0
	if isTransient {
		shortBlocks = M
	}

	// Build the per-channel in buffer (CC*(N+overlap)): the overlap prefix comes
	// from prefilter_mem (zero on a fresh encoder's first frame) and the body is
	// the pre-emphasised input. celt_preemphasis writes into in[c*(N+overlap)+overlap].
	in := make([]int32, CC*(N+overlap))
	for c := 0; c < CC; c++ {
		e.preemphasis(pcm[c:], in[c*(N+overlap)+overlap:], N, CC, c)
	}

	freq = make([]int32, CC*N)
	e.computeMDCTs(shortBlocks, in, freq, C, CC, LM)

	bandE = make([]int32, nbEBands*CC)
	ComputeBandEnergies(freq, e.eBands, e.logN, bandE, nbEBands, shortMdctSize, effEnd, C, LM)

	X = make([]int32, C*N)
	NormaliseBands(freq, X, bandE, e.eBands, nbEBands, shortMdctSize, effEnd, C, M)
	return freq, bandE, X
}

// preemphasis ports libopus celt_preemphasis for the static 48000/960 mode
// (coef[1]==0, !clip). With upsample==1 it takes the fast path: inp[i] = x - m
// and m = MULT16_32_Q15(coef0, x), where x = RES2SIG(INT16TORES(pcmp[CC*i])).
// With upsample>1 (sub-48 kHz API rate) it zero-stuffs the input up to the 48
// kHz core rate: inp is cleared, the Nu input samples are scattered into
// inp[i*upsample], then the same pre-emphasis recurrence runs over all N
// (zero-stuffed) samples. It advances st->preemph_memE[c].
func (e *CELTEncoder) preemphasis(pcmp []int16, inp []int32, N, CC, c int) {
	coef0 := staticMDCT48000Preemph0
	m := e.preemphMemE[c]
	upsample := e.upsample
	if upsample <= 1 {
		for i := 0; i < N; i++ {
			x := res2sig(int16ToRes(pcmp[CC*i]))
			inp[i] = x - m
			m = mult16x32q15(coef0, x)
		}
		e.preemphMemE[c] = m
		return
	}
	for i := 0; i < N; i++ {
		inp[i] = 0
	}
	Nu := N / upsample
	for i := 0; i < Nu; i++ {
		inp[i*upsample] = res2sig(int16ToRes(pcmp[CC*i]))
	}
	for i := 0; i < N; i++ {
		x := inp[i]
		inp[i] = x - m
		m = mult16x32q15(coef0, x)
	}
	e.preemphMemE[c] = m
}

// computeMDCTs ports the (static) compute_mdcts from celt_encoder.c for the
// FIXED_POINT non-QEXT build: it windows/forward-MDCTs every sub-frame for each
// channel into the interleaved out, then for a downmixed CC==2,C==1 frame
// averages the two channels' MDCTs. With st->upsample>1 (sub-48 kHz API rate)
// it then scales the lowest B*N/upsample bins per channel by upsample and zeros
// the upper bins, dropping the spectral images introduced by zero-stuffing.
func (e *CELTEncoder) computeMDCTs(shortBlocks int, in, out []int32, C, CC, LM int) {
	overlap := celtOverlap
	var N, B, shift int
	if shortBlocks != 0 {
		B = shortBlocks
		N = celtShortMdctSize
		shift = celtMaxLM
	} else {
		B = 1
		N = celtShortMdctSize << LM
		shift = celtMaxLM - LM
	}
	sc := e.scratch
	for c := 0; c < CC; c++ {
		for b := 0; b < B; b++ {
			e.mdct.MDCTForward(
				in[c*(B*N+overlap)+b*N:],
				out[b+c*N*B:],
				e.window, overlap, shift, B, sc)
		}
	}
	if CC == 2 && C == 1 {
		for i := 0; i < B*N; i++ {
			out[i] = add32(half32(out[i]), half32(out[B*N+i]))
		}
	}
	upsample := e.upsample
	if upsample > 1 {
		bound := B * N / upsample
		for c := 0; c < C; c++ {
			base := c * B * N
			for i := 0; i < bound; i++ {
				out[base+i] *= int32(upsample)
			}
			for i := bound; i < B*N; i++ {
				out[base+i] = 0
			}
		}
	}
}

//go:build gopus_fixed_point

package fixedpoint

// This file ports the FIXED_POINT (ENABLE_RES24) CELT encoder front-end for the
// static 48000/960 mode and, with ENABLE_QEXT, the native 96000/1920 mode. It
// applies forward pre-emphasis (celt_preemphasis), windowed forward MDCTs
// (compute_mdcts) for normal and transient blocks, then computes band energies
// and normalises the bands. These stages produce the interleaved post-MDCT
// signal (freq), per-band energies (bandE) and normalised bands (X) consumed by
// celt_encode_with_ec before quant_all_bands. The frame driver and its
// transient, prefilter, allocation and quantisation stages are in celt_encode.go.

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
// static 48000/960 and 96000/1920 modes. It owns per-channel pre-emphasis memory
// and mode geometry, mirroring the libopus OpusCustomEncoder fields it touches.
type CELTEncoder struct {
	channels       int
	streamChannels int32
	analysis       CELTAnalysisInfo

	// Static mode geometry. Sub-48 kHz API rates use the 48 kHz mode with
	// zero-stuffing; 96 kHz selects the native 240-sample mode in ENABLE_QEXT.
	modeFs        int
	shortMdctSize int
	overlap       int
	maxLM         int
	effEBands     int
	maxPeriod     int
	qextScale     int
	preemph0      int16
	preemph1      int16
	preemph2      int16
	preemph2Q30   int32

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
	lossRate   int32

	// SilkInfo and prediction controls mirror CELTEncoder state in
	// celt/celt_encoder.c. signalType and offset are opus_int32 fields; the
	// prediction controls remain booleans after CELT_SET_PREDICTION maps 0..2.
	silkSignalType   int32
	silkOffset       int32
	forceIntra       bool
	disablePrefilter bool

	// bitrate is st->bitrate (bits/s), OPUS_BITRATE_MAX when unset.
	bitrate int

	// vbr / constrainedVBR mirror st->vbr and st->constrained_vbr. constrainedVBR
	// defaults to 1 (the celt_encoder_init default).
	vbr              bool
	constrainedVBR   bool
	customSignalling bool // celt_encode_with_ec st->signalling for opus_custom_encode.

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
	// shortPCMRes carries the int16 API's INT16TORES conversion into the
	// ENABLE_RES24 Q8 encoder without narrowing a direct opus_res input.
	shortPCMRes []int32

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

	mdct         *MDCTLookup
	customTables fixedCustomTables
	window       []int16
	eBands       []int16
	logN         []int16
	qext         celtQEXTState
	finalRange   uint32

	// scratch holds the reusable per-frame/per-band encode working buffers,
	// grown once and reused across every frame of a packet.
	scratch *celtEncodeScratch
}

// CELTAnalysisInfo carries the fields consumed by the fixed-point CELT encoder.
// The values retain AnalysisInfo's float width even in a FIXED_POINT build;
// celt/celt_encoder.c uses float analysis for trim, pitch, allocation and VBR.
type CELTAnalysisInfo struct {
	Valid         bool
	Bandwidth     int32
	LeakBoost     [19]uint8
	Activity      float32
	Tonality      float32
	TonalitySlope float32
	MaxPitchRatio float32
}

// SetAnalysisInfo mirrors CELT_SET_ANALYSIS for the current subframe. An
// invalid value clears the prior frame's analysis decisions.
func (e *CELTEncoder) SetAnalysisInfo(info CELTAnalysisInfo) {
	e.analysis = info
}

// SetLSBDepth mirrors CELT_SET_LSB_DEPTH_REQUEST. opus_encode_native passes the
// per-call API input depth to CELT after opus_encode clamps short input to 16.
func (e *CELTEncoder) SetLSBDepth(depth int) {
	e.lsbDepth = depth
}

// SetSilkInfo mirrors CELT_SET_SILK_INFO for hybrid CELT transient and rate
// decisions (celt/celt_encoder.c).
func (e *CELTEncoder) SetSilkInfo(signalType, offset int32) {
	e.silkSignalType = signalType
	e.silkOffset = offset
}

// SetPrediction mirrors CELT_SET_PREDICTION: mode 0 disables prediction and
// forces intra energy coding, mode 1 disables the prefilter, and mode 2 enables
// normal prediction.
func (e *CELTEncoder) SetPrediction(mode int32) {
	if mode < 0 {
		mode = 0
	} else if mode > 2 {
		mode = 2
	}
	e.disablePrefilter = mode <= 1
	e.forceIntra = mode == 0
}

// NewCELTEncoder allocates and resets an integer CELT encoder front-end for the
// static 48000/960 mode with the given channel count (1 or 2). All cross-frame
// state (pre-emphasis memory) starts at zero, matching celt_encoder_init.
func NewCELTEncoder(channels int) *CELTEncoder {
	return NewCELTEncoderRate(channels, 48000)
}

// NewCELTEncoderRate allocates and resets an integer CELT encoder front-end for
// the static 48000/960 mode at API rates through 48 kHz, and the native
// 96000/1920 mode at 96 kHz. The caller passes API-rate frame sizes; lower
// rates use the 48 kHz mode's zero-stuffing factor.
func NewCELTEncoderRate(channels, sampleRate int) *CELTEncoder {
	modeFs := 48000
	shortMdctSize := celtShortMdctSize
	overlap := celtOverlap
	maxPeriod := combFilterMaxPeriod
	qextScale := 1
	preemph0 := staticMDCT48000Preemph0
	var preemph1 int16
	var preemph2Q30 int32
	if sampleRate == 96000 && fixedQEXTBuild {
		modeFs = 96000
		shortMdctSize = 240
		overlap = 240
		maxPeriod *= 2
		qextScale = 2
		preemph0 = 30245 // celt/static_modes_fixed.h mode96000_1920_240 preemph[0]
		preemph1 = 7209  // celt/static_modes_fixed.h mode96000_1920_240 preemph[1]
		const preemph2, preemph3 = int16(6197), int16(5415)
		// celt/celt_encoder.c computes coef2_q30 from the exact fixed mode
		// coefficients with one Newton step before the two-tap pre-emphasis.
		residual := int32(1<<25) - mult16x16(int32(preemph3), int32(preemph2))
		preemph2Q30 = shl32(int32(preemph2), 18) + pshr32(mult16x16(residual, int32(preemph2)), 7)
	}
	e := &CELTEncoder{
		channels:       channels,
		streamChannels: int32(channels),
		upsample:       resamplingFactor(sampleRate),
		modeFs:         modeFs,
		shortMdctSize:  shortMdctSize,
		overlap:        overlap,
		maxLM:          celtMaxLM,
		effEBands:      celtNbEBands,
		maxPeriod:      maxPeriod,
		qextScale:      qextScale,
		preemph0:       preemph0,
		preemph1:       preemph1,
		preemph2Q30:    preemph2Q30,
		start:          0,
		end:            celtNbEBands,
		complexity:     5,
		lsbDepth:       24,
		bitrate:        opusBitrateMax,
		preemphMemE:    make([]int32, channels),
		mdct:           NewStaticMDCTLookup48000(),
		window:         staticMDCT48000Window[:],
		eBands:         staticMDCT48000EBands[:],
		logN:           staticMDCT48000LogN[:],
	}
	e.inMem = make([]int32, channels*overlap)
	e.prefilterMem = make([]int32, channels*maxPeriod)
	e.oldBandE = make([]int32, 2*celtNbEBands)
	e.oldLogE = make([]int32, 2*celtNbEBands)
	e.oldLogE2 = make([]int32, 2*celtNbEBands)
	e.energyError = make([]int32, 2*celtNbEBands)
	e.initQEXTState(channels)
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

// SetPacketLoss mirrors OPUS_SET_PACKET_LOSS_PERC for the CELT prefilter and
// coarse energy decisions in celt/celt_encoder.c.
func (e *CELTEncoder) SetPacketLoss(percent int) { e.lossRate = int32(percent) }

// SetBitrate sets st->bitrate in bits/s (OPUS_SET_BITRATE_REQUEST).
func (e *CELTEncoder) SetBitrate(b int) { e.bitrate = b }

// Bitrate reports the persistent CELT target bitrate control.
func (e *CELTEncoder) Bitrate() int { return e.bitrate }

// SetStreamChannels mirrors CELT_SET_CHANNELS: CELT codes one or both of the
// encoder's input channels while retaining the full input for stereo analysis.
func (e *CELTEncoder) SetStreamChannels(channels int32) {
	if channels >= 1 && channels <= int32(e.channels) {
		e.streamChannels = channels
	}
}

// SetVBR enables/disables variable bitrate (OPUS_SET_VBR_REQUEST).
func (e *CELTEncoder) SetVBR(v bool) { e.vbr = v }

// SetConstrainedVBR sets st->constrained_vbr (OPUS_SET_VBR_CONSTRAINT_REQUEST).
func (e *CELTEncoder) SetConstrainedVBR(v bool) { e.constrainedVBR = v }

// SetCustomSignalling mirrors st->signalling for opus_custom_encode. The
// signalled header consumes one byte from finite bitrate accounting while the
// caller-provided range-coder buffer already excludes the header.
func (e *CELTEncoder) SetCustomSignalling(enabled bool) {
	e.customSignalling = enabled
}

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

// Reset clears the CELT frame state while preserving controls that sit before
// ENCODER_RESET_START in celt/celt_encoder.c, including band range, prediction,
// bitrate, VBR and stream channel count.
func (e *CELTEncoder) Reset() {
	e.resetQEXTState()
	e.finalRange = 0
	e.rng = 0
	e.spreadDecision = spreadNormal
	e.delayedIntra = 1
	e.specAvg = 0
	e.intensity = 0
	e.lastCodedBands = 0
	e.stereoSaving = 0
	e.consecTransient = 0
	e.prefilterPeriod = 0
	e.prefilterGain = 0
	e.prefilterTapset = 0
	e.spreading = SpreadingState{TonalAverage: 256}
	e.overlapMax = 0
	e.vbrReservoir = 0
	e.vbrDrift = 0
	e.vbrOffset = 0
	e.vbrCount = 0
	e.analysis = CELTAnalysisInfo{}
	e.silkSignalType = 0
	e.silkOffset = 0
	e.energyMask = nil
	clear(e.preemphMemE)
	clear(e.inMem)
	clear(e.prefilterMem)
	clear(e.oldBandE)
	clear(e.energyError)
	for i := range e.oldLogE {
		e.oldLogE[i] = -gconst(28)
		e.oldLogE2[i] = -gconst(28)
	}
}

// FinalRange returns the range coder state from the last completed frame.
func (e *CELTEncoder) FinalRange() uint32 { return e.finalRange }

// FrontEnd ports the input -> normalized bands stage of celt_encode_with_ec for
// the static 48000/960 mode. pcm is channels*frameSize interleaved int16 PCM,
// frameSize the 48k-core per-channel sample count (shortMdctSize<<LM), and
// isTransient selects the transient MDCT striping (shortBlocks==M) over the
// normal long-block path. It returns the interleaved post-MDCT freq (celt_sig),
// the per-band bandE (celt_ener, channel-major) and the normalised X
// (celt_norm, interleaved), advancing the per-channel pre-emphasis memory.
func (e *CELTEncoder) FrontEnd(pcm []int16, frameSize int, isTransient bool) (freq, bandE, X []int32) {
	nbEBands := celtNbEBands
	overlap := e.overlap
	shortMdctSize := e.shortMdctSize
	C := e.channels
	CC := e.channels

	LM := 0
	for LM = 0; LM <= e.maxLM; LM++ {
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
	pcmRes := make([]int32, len(pcm))
	for i, sample := range pcm {
		pcmRes[i] = int16ToRes(sample)
	}
	for c := 0; c < CC; c++ {
		e.preemphasis(pcmRes[c:], in[c*(N+overlap)+overlap:], N, CC, c, false)
	}

	freq = make([]int32, CC*N)
	e.computeMDCTs(shortBlocks, in, freq, C, CC, LM)

	bandE = make([]int32, nbEBands*CC)
	ComputeBandEnergies(freq, e.eBands, e.logN, bandE, nbEBands, shortMdctSize, effEnd, C, LM)

	X = make([]int32, C*N)
	NormaliseBands(freq, X, bandE, e.eBands, nbEBands, shortMdctSize, effEnd, C, M)
	return freq, bandE, X
}

// preemphasis ports libopus celt_preemphasis for the selected static mode.
// The 48 kHz mode uses its one-tap fast path; the native 96 kHz mode uses the
// ENABLE_QEXT two-tap recurrence. Lower API rates zero-stuff to the 48 kHz
// mode before applying the one-tap recurrence.
func (e *CELTEncoder) preemphasis(pcmp []int32, inp []int32, N, CC, c int, needClip bool) {
	coef0 := e.preemph0
	m := e.preemphMemE[c]
	upsample := e.upsample
	if upsample <= 1 && e.preemph1 == 0 && !needClip {
		for i := 0; i < N; i++ {
			x := res2sig(pcmp[CC*i])
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
		inp[i*upsample] = res2sig(pcmp[CC*i])
	}
	if needClip {
		const limit = int32(65536 << sigShift)
		for i := 0; i < Nu; i++ {
			if inp[i*upsample] < -limit {
				inp[i*upsample] = -limit
			} else if inp[i*upsample] > limit {
				inp[i*upsample] = limit
			}
		}
	}
	for i := 0; i < N; i++ {
		x := inp[i]
		if e.preemph1 != 0 {
			var tmp int32
			if fixedQEXTBuild {
				tmp = shl32(mult32x32q31(e.preemph2Q30, x), 1)
			} else {
				// celt/celt_encoder.c celt_preemphasis() uses the Q15
				// MULT16_32 path without ENABLE_QEXT.
				tmp = shl32(mult16x32q15(e.preemph2, x), 15-sigShift)
			}
			inp[i] = tmp + m
			m = mult16x32q15(e.preemph1, inp[i]) - mult16x32q15(coef0, tmp)
		} else {
			inp[i] = x - m
			m = mult16x32q15(coef0, x)
		}
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
	overlap := e.overlap
	var N, B, shift int
	if shortBlocks != 0 {
		B = shortBlocks
		N = e.shortMdctSize
		shift = e.maxLM
	} else {
		B = 1
		N = e.shortMdctSize << LM
		shift = e.maxLM - LM
	}
	sc := e.ensureScratch()
	for c := 0; c < CC; c++ {
		for b := 0; b < B; b++ {
			e.mdctForward(
				in[c*(B*N+overlap)+b*N:],
				out[b+c*N*B:], overlap, shift, B, sc)
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

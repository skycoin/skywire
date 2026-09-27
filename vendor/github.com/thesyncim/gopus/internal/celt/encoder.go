// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the encoder struct that mirrors the decoder state
// for synchronized prediction.

package celt

import (
	"github.com/thesyncim/gopus/internal/arena"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const opusBitrateMax = -1

// Encoder encodes audio frames using CELT transform coding.
// It maintains state across frames for proper audio continuity via energy
// prediction and overlap-add analysis.
//
// The encoder state mirrors the decoder state to ensure synchronized
// prediction. This includes:
// - Energy arrays for inter-frame prediction
// - Overlap buffer for MDCT overlap-add
// - Pre-emphasis filter state
// - RNG state for deterministic folding decisions
//
// Reference: RFC 6716 Section 4.3
type Encoder struct {
	// Range encoder reference (set per frame)
	rangeEncoder *rangecoding.Encoder

	// Configuration (mirrors decoder)
	channels       int32         // libopus CELTEncoder.channels
	streamChannels int32         // coded channels, mirrors CELT_SET_CHANNELS
	sampleRate     int32         // Always 48000
	lsbDepth       int32         // Input LSB depth (8-24 bits)
	bandwidth      CELTBandwidth // Active bandwidth cap (NB..FB)
	// upsample mirrors libopus CELTEncoder.upsample = resampling_factor(Fs):
	// 1 at 48 kHz, 2/3/4/6 at 24/16/12/8 kHz. EncodeFrame multiplies the API-rate
	// frame size by upsample to the 48 kHz core block and zero-stuffs the input in
	// pre-emphasis (celt_encode_with_ec frame_size *= st->upsample; celt_preemphasis
	// with Nu = N/upsample). 0/1 both mean no upsampling.
	upsample int32

	// Energy state (persists across frames, mirrors decoder)
	prevEnergy  []celtGLog // Previous frame band energies [MaxBands * channels]
	prevEnergy2 []celtGLog // Two frames ago energies (for anti-collapse)
	energyError []celtGLog // Previous coarse quantization residuals [MaxBands * channels]

	// Analysis state for overlap (mirrors decoder's synthesis state)
	overlapBuffer []celtSig // MDCT overlap [Overlap * channels]
	preemphState  []celtSig // Pre-emphasis filter state [channels]
	// hd96kOverlap and hd96kPreemph hold the native 96 kHz HD analysis
	// parameters (overlap 240, 2-tap HD pre-emphasis). hd96kOverlap == 0
	// selects the 48 kHz path (Overlap constant), keeping the default build
	// unchanged. Set by EnableHD96kMode (gopus_qext).
	hd96kOverlap int
	hd96kPreemph [4]float32
	// customScaleBase is the mode short-MDCT size used to scale band-bin edges
	// for a non-standard Opus Custom mode in the Fs==400*shortMdctSize family.
	// Zero selects the 48 kHz base (Overlap=120), keeping the default and 48 kHz
	// paths byte-identical. When non-zero the band-bin scale is
	// frameSize/customScaleBase == 1<<LM, matching libopus eBands[i]<<LM.
	customScaleBase int
	// customEffBands is the effEBands clamp for the active custom mode (0 selects
	// the standard 21-band clamp).
	customEffBands int
	// perMode carries the per-mode CELT tables (band edges, widths, logN,
	// allocVectors, pulse cache) for a non-standard Opus Custom mode whose band
	// layout differs from the static 21-band 48 kHz tables (e.g. 48000/640,
	// nbEBands=19). It is nil for the standard, family, hybrid and QEXT paths,
	// which then stay byte-identical. Populated only by EnablePerModeTables,
	// called from the gopus_custom_modes celt<->custom plumbing.
	perMode *perModeTables
	// overlapMax mirrors libopus st->overlap_max for CELT silence detection.
	// It tracks max absolute amplitude over the last overlap region.
	overlapMax opusVal32

	// RNG state (for deterministic folding decisions)
	rng uint32

	// Frame counting for intra mode decisions
	frameCount int32 // Number of frames encoded (0 = first frame uses intra mode)
	// Coarse-energy intra/inter decision state (libopus delayedIntra).
	delayedIntra opusVal32
	forceIntra   bool
	// CELT prediction control mirrors CELT_SET_PREDICTION:
	// 0 => force_intra=1, disable_pf=1
	// 1 => force_intra=0, disable_pf=1
	// 2 => force_intra=0, disable_pf=0
	disablePrefilter bool
	// Consecutive transient frames (used for anti-collapse flag)
	consecTransient int32

	// Allocation history for skip decisions
	lastCodedBands int32 // Previous coded band count (0 = uninitialized)
	intensity      int32 // Previous intensity stereo decision (libopus hysteresis state)

	// Bitrate control
	targetBitrate int32 // Target bitrate in bits per second (0 = use buffer size)
	frameBits     int32 // Per-frame bit budget for coarse energy (set during encoding)
	// coarseAvailableBytes mirrors libopus quant_coarse_energy() nbAvailableBytes.
	// When >0, it overrides budget/8 for coarse intra/decay decisions.
	coarseAvailableBytes int32
	maxPayloadBytes      int32 // Optional per-frame payload cap (excludes TOC byte)
	vbr                  bool
	constrainedVBR       bool
	// constrainedVBRBoundScale scales libopus vbr_bound for constrained-VBR
	// max-allowed computation. 1.0 matches libopus single-stream behavior.
	constrainedVBRBoundScale opusVal16
	// Constrained-VBR state mirrors libopus CELT encoder cadence.
	// Units are Q3 bits unless noted.
	vbrReservoir int32
	vbrOffset    int32
	vbrDrift     int32
	vbrCount     int32

	encoderQEXTFields

	// Complexity control (0-10)
	complexity int32

	// Spread decision state (persistent across frames for hysteresis)
	spreadDecision int32 // Current spread decision (0-3)
	tonalAverage   int32 // Running average for spread decision hysteresis
	hfAverage      int32 // High frequency average for tapset decision
	tapsetDecision int32 // Tapset decision (0, 1, or 2)

	// Tonality analysis state (for VBR decisions)
	prevBandLogEnergy []celtGLog // Previous frame log-energy per band for spectral flux
	lastTonality      opusVal16  // Running average tonality for smoothing
	lastStereoSaving  opusVal16  // Running stereo_saving estimate from alloc_trim analysis
	lastPitchChange   bool       // Previous frame pitch_change flag for VBR targeting
	specAvg           celtGLog   // Smoothed spectral average for temporal VBR (libopus st->spec_avg)
	lastTemporalVBR   celtGLog   // Previous frame's temporal_vbr for VBR target adjustment
	lastTellFrac      int        // Previous frame's ec_tell_frac at VBR point (for tell estimation)

	// Analysis bandwidth state used by bit allocation gating.
	// This mirrors libopus use of st->analysis.bandwidth for clt_compute_allocation().
	analysisBandwidth     int  // 1..20 bandwidth index from previous frame analysis
	analysisValid         bool // True after at least one analysis update
	analysisActivity      opusVal16
	analysisLeakBoost     [leakBands]uint8
	analysisTonality      opusVal16
	analysisTonalitySlope opusVal16
	analysisMaxPitchRatio opusVal16
	// Surround trim adjustment (in trim units) used by alloc_trim analysis.
	// This mirrors libopus alloc_trim_analysis() surround_trim contribution.
	surroundTrim celtGLog

	// energyMask stores per-band surround masking provided by multistream control.
	// Layout matches libopus OPUS_SET_ENERGY_MASK: [21] for mono, [42] for stereo.
	energyMask []celtGLog

	// Dynamic allocation analysis state (for VBR decisions)
	// These are computed from the previous frame and used for current frame's VBR target.
	// Reference: libopus celt_encoder.c dynalloc_analysis()
	lastDynalloc DynallocResult

	// Hybrid mode flag
	// When true, postfilter flag encoding is skipped per RFC 6716 Section 3.2
	// Reference: libopus celt_encoder.c line 2047-2048
	hybrid bool
	// SILK side information forwarded by the Opus hybrid wrapper.
	// libopus uses this in hybrid CELT for weak-transient gating and
	// bitrate targeting. The generic EncodeFrame path only needs the
	// weak-transient part for transition prefill parity.
	silkSignalType int
	silkOffset     int

	// LFE mode flag.
	// When true, encoder applies low-frequency-effects constraints.
	lfe bool

	// Phase inversion disabled for stereo encoding
	// When true, disables stereo phase inversion decorrelation
	phaseInversionDisabled bool

	// DC rejection filter state (high-pass filter to remove DC offset)
	// libopus applies this at the Opus encoder level before CELT processing
	// Reference: libopus src/opus_encoder.c dc_reject()
	hpMem []opusVal32 // High-pass filter memory [channels]

	// dcRejectEnabled controls whether EncodeFrame applies dc_reject().
	// When CELT is driven by the Opus encoder, dc_reject is already applied,
	// so this should be false to avoid double filtering.
	dcRejectEnabled bool

	// lsbQuantizationEnabled controls whether EncodeFrame rounds input samples
	// to the configured LSB depth before any CELT-local preprocessing.
	// Top-level Opus encoding already does this before dc_reject, so CELT must
	// skip it there to avoid perturbing the filtered samples.
	lsbQuantizationEnabled bool

	// delayCompensationEnabled controls whether EncodeFrame prepends the
	// Fs/250 CELT lookahead history. Standalone CELT defaults this to true;
	// top-level Opus wiring should disable it to avoid double-compensation.
	delayCompensationEnabled bool

	// Delay buffer for lookahead compensation (matches libopus delay_compensation)
	// libopus uses Fs/250 = 192 samples at 48kHz for delay compensation.
	// This provides a 4ms lookahead that allows for better transient handling.
	// Reference: libopus src/opus_encoder.c delay_compensation
	delayBuffer []opusRes // Size = delayCompensation * channels

	// Prefilter (comb filter) state for postfilter signaling.
	// These mirror libopus CELT encoder fields used by run_prefilter().
	prefilterPeriod int
	prefilterGain   float32
	prefilterTapset int
	prefilterMem    []celtSig
	// Packet loss expectation (0-100) for prefilter gain scaling.
	packetLoss int32

	// Last frame's band energies retained for dynalloc analysis.
	lastBandLogE  []celtGLog // bandLogE (primary MDCT energies)
	lastBandLogE2 []celtGLog // bandLogE2 (secondary MDCT for transients)

	// Scratch buffers for hot path to eliminate heap allocations
	scratch encoderScratch

	// Scratch buffers for band encoding (PVQ, theta RDO, etc.)
	bandEncScratch bandEncodeScratch

	// Scratch buffers for tonality analysis (zero-alloc)
	tonalityScratch tonalityScratch

	// Scratch buffers for TF analysis (zero-alloc)
	tfScratch TFAnalysisScratch

	// Scratch buffers for dynalloc analysis (zero-alloc)
	dynallocScratch DynallocScratch
}

// NewEncoder creates a new CELT encoder with the given number of channels.
// Valid channel counts are 1 (mono) or 2 (stereo).
// The encoder is ready to process CELT frames after creation.
//
// The initialization mirrors libopus encoder reset state:
// - prevEnergy starts at 0.0 (oldBandE cleared)
// - RNG seed 0 (matches libopus initialization)
func NewEncoder(channels int) *Encoder {
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	e := &Encoder{
		channels:       int32(channels),
		streamChannels: int32(channels),
		sampleRate:     48000, // CELT always operates at 48kHz internally
		lsbDepth:       24,    // Default to full 24-bit depth
		bandwidth:      CELTFullband,

		// Allocate energy arrays for all bands and channels
		prevEnergy:  make([]celtGLog, MaxBands*channels),
		prevEnergy2: make([]celtGLog, MaxBands*channels),
		energyError: make([]celtGLog, MaxBands*channels),

		// Overlap buffer for MDCT overlap-add analysis
		// Size is Overlap (120) samples per channel
		overlapBuffer: make([]celtSig, Overlap*channels),

		// Pre-emphasis filter state, one per channel
		preemphState: make([]celtSig, channels),

		// Initialize RNG to zero (libopus default)
		rng: 0,

		// Complexity defaults to max quality (libopus default)
		complexity: 10,
		// libopus initializes delayedIntra to 1.
		delayedIntra: 1.0,

		// Initialize spread decision state (libopus defaults to SPREAD_NORMAL)
		// Reference: libopus celt_encoder.c line 3088-3089
		spreadDecision: spreadNormal,
		tonalAverage:   256, // libopus initializes to 256
		hfAverage:      0,
		tapsetDecision: 0,

		// Initialize tonality analysis state
		prevBandLogEnergy:     make([]celtGLog, MaxBands*channels),
		lastTonality:          opusVal16(0.5), // Start with neutral tonality estimate
		lastStereoSaving:      0.0,
		lastPitchChange:       false,
		analysisBandwidth:     20,
		analysisValid:         false,
		analysisActivity:      0.0,
		analysisTonality:      0.0,
		analysisTonalitySlope: 0.0,
		analysisMaxPitchRatio: 0.0,

		// DC rejection (high-pass) filter memory, one per channel
		hpMem: make([]opusVal32, channels),

		// Apply dc_reject by default for standalone CELT usage
		dcRejectEnabled: true,

		// Standalone CELT also owns the initial LSB-depth rounding.
		lsbQuantizationEnabled: true,

		// Standalone CELT defaults to Opus-style delay compensation.
		// Top-level Opus integration should disable this to avoid double-applying.
		delayCompensationEnabled: true,

		// Delay buffer for lookahead (192 samples at 48kHz = 4ms)
		// This matches libopus delay_compensation
		delayBuffer: make([]opusRes, DelayCompensation*channels),

		// Prefilter state (comb filter history) for postfilter signaling.
		// libopus zero-initializes this state on reset.
		prefilterPeriod: 0,
		prefilterGain:   0,
		prefilterTapset: 0,
		prefilterMem:    make([]celtSig, combFilterMaxPeriod*channels),

		// Default to VBR enabled to mirror libopus behavior.
		vbr:                      true,
		constrainedVBRBoundScale: 1.0,
	}

	// Energy arrays default to zero after allocation (matches libopus init).

	return e
}

// SetAnalysisBandwidth provides the analysis-derived bandwidth index (1..20)
// used by allocation gating in clt_compute_allocation().
func (e *Encoder) SetAnalysisBandwidth(bandwidth int, valid bool) {
	if !valid {
		e.analysisValid = false
		e.analysisActivity = 0
		e.analysisTonality = 0
		e.analysisTonalitySlope = 0
		e.analysisMaxPitchRatio = 0
		for i := range e.analysisLeakBoost {
			e.analysisLeakBoost[i] = 0
		}
		return
	}
	if bandwidth < 1 {
		bandwidth = 1
	}
	if bandwidth > 20 {
		bandwidth = 20
	}
	e.analysisBandwidth = bandwidth
	e.analysisValid = true
	e.analysisActivity = 0
	e.analysisTonality = 0
	e.analysisTonalitySlope = 0
	e.analysisMaxPitchRatio = 1.0
	for i := range e.analysisLeakBoost {
		e.analysisLeakBoost[i] = 0
	}
}

// SetAnalysisInfo provides analysis-derived state from the top-level Opus analysis
// pipeline. This mirrors libopus use of AnalysisInfo in CELT dynalloc.
func (e *Encoder) SetAnalysisInfo(bandwidth int, leakBoost [leakBands]uint8, activity, tonalitySlope opusVal16, maxPitchRatio opusVal16, valid bool) {
	e.SetAnalysisInfoWithTonality(bandwidth, leakBoost, activity, 0, tonalitySlope, maxPitchRatio, valid)
}

// SetAnalysisInfoWithTonality provides the full AnalysisInfo subset used by
// libopus CELT decisions, including analysis.tonality for compute_vbr().
func (e *Encoder) SetAnalysisInfoWithTonality(bandwidth int, leakBoost [leakBands]uint8, activity, tonality, tonalitySlope opusVal16, maxPitchRatio opusVal16, valid bool) {
	if !valid {
		e.SetAnalysisBandwidth(0, false)
		return
	}
	if bandwidth < 1 {
		bandwidth = 1
	}
	if bandwidth > 20 {
		bandwidth = 20
	}
	e.analysisBandwidth = bandwidth
	e.analysisValid = true
	e.analysisActivity = activity
	e.analysisLeakBoost = leakBoost
	if tonality < 0 {
		tonality = 0
	} else if tonality > 1 {
		tonality = 1
	}
	e.analysisTonality = tonality
	e.analysisTonalitySlope = tonalitySlope
	if maxPitchRatio < 0 {
		maxPitchRatio = 0
	}
	if maxPitchRatio > 1 {
		maxPitchRatio = 1
	}
	e.analysisMaxPitchRatio = maxPitchRatio
}

// AnalysisBandwidth returns the current analysis-derived bandwidth index.
func (e *Encoder) AnalysisBandwidth() int {
	return e.analysisBandwidth
}

func (e *Encoder) dynallocLeakBoost() []uint8 {
	leak := e.analysisLeakBoost[:]
	if !e.analysisValid {
		return leak
	}
	return leak
}

// Reset clears encoder state for a new stream.
// Call this when starting to encode a new audio stream.
func (e *Encoder) Reset() {
	// Clear energy arrays (match libopus reset: oldBandE=0).
	for i := range e.prevEnergy {
		e.prevEnergy[i] = 0
		e.prevEnergy2[i] = 0
		e.energyError[i] = 0
	}

	// Clear overlap buffer
	for i := range e.overlapBuffer {
		e.overlapBuffer[i] = 0
	}
	e.overlapMax = 0

	// Reset prefilter state
	e.prefilterPeriod = 0
	e.prefilterGain = 0
	e.prefilterTapset = 0
	for i := range e.prefilterMem {
		e.prefilterMem[i] = 0
	}
	// Clear pre-emphasis state
	for i := range e.preemphState {
		e.preemphState[i] = 0
	}

	// Reset RNG to zero (libopus default)
	e.rng = 0

	// Clear range encoder reference
	e.rangeEncoder = nil

	// Reset frame counter
	e.frameCount = 0
	e.frameBits = 0
	e.coarseAvailableBytes = 0
	e.maxPayloadBytes = 0
	e.delayedIntra = 1.0
	e.lastCodedBands = 0
	e.intensity = 0
	e.consecTransient = 0
	e.vbrReservoir = 0
	e.vbrOffset = 0
	e.vbrDrift = 0
	e.vbrCount = 0
	e.clearLastQEXTPayload()

	// Reset spread decision state (match libopus init values)
	// Reference: libopus celt_encoder.c line 3088-3089
	e.spreadDecision = spreadNormal
	e.tonalAverage = 256
	e.hfAverage = 0
	e.tapsetDecision = 0

	// Reset tonality analysis state
	for i := range e.prevBandLogEnergy {
		e.prevBandLogEnergy[i] = 0
	}
	e.lastTonality = opusVal16(0.5)
	e.lastStereoSaving = 0
	e.lastPitchChange = false
	e.analysisBandwidth = 20
	e.analysisValid = false
	e.analysisActivity = 0
	e.analysisTonality = 0
	e.analysisTonalitySlope = 0
	e.analysisMaxPitchRatio = 0
	for i := range e.analysisLeakBoost {
		e.analysisLeakBoost[i] = 0
	}
	e.surroundTrim = 0
	if len(e.energyMask) > 0 {
		clear(e.energyMask)
		e.energyMask = e.energyMask[:0]
	}

	// Clear DC rejection filter state
	for i := range e.hpMem {
		e.hpMem[i] = 0
	}

	// Clear delay buffer
	for i := range e.delayBuffer {
		e.delayBuffer[i] = 0
	}

}

// SetSurroundTrim sets the surround trim adjustment used by alloc_trim analysis.
// Positive values reduce alloc_trim (favoring higher bands), matching libopus.
func (e *Encoder) SetSurroundTrim(trim celtGLog) {
	e.surroundTrim = trim
}

// SurroundTrim returns the current surround trim adjustment.
func (e *Encoder) SurroundTrim() celtGLog {
	return e.surroundTrim
}

// SetEnergyMask sets per-band surround masking for CELT surround control.
// Expected sizes: 21 values for mono, 42 values for stereo.
// Invalid sizes clear the mask.
func (e *Encoder) SetEnergyMask(mask []float32) {
	needed := MaxBands * int(e.channels)
	if needed <= 0 || len(mask) < needed {
		if len(e.energyMask) > 0 {
			clear(e.energyMask)
			e.energyMask = e.energyMask[:0]
		}
		return
	}
	if cap(e.energyMask) < needed {
		e.energyMask = make([]celtGLog, needed)
	} else {
		e.energyMask = e.energyMask[:needed]
	}
	copy(e.energyMask, mask[:needed])
}

// EnergyMask returns the current per-band surround mask.
func (e *Encoder) EnergyMask() []float32 {
	out := make([]float32, len(e.energyMask))
	copy(out, e.energyMask)
	return out
}

// SetComplexity sets encoder complexity (0-10).
// Higher values use more CPU for better quality.
func (e *Encoder) SetComplexity(complexity int) {
	if complexity < 0 {
		complexity = 0
	}
	if complexity > 10 {
		complexity = 10
	}
	e.complexity = int32(complexity)
}

// SetVBR enables or disables variable bitrate mode.
func (e *Encoder) SetVBR(enabled bool) {
	e.vbr = enabled
}

// VBR reports whether variable bitrate mode is enabled.
func (e *Encoder) VBR() bool {
	return e.vbr
}

// SetConstrainedVBR enables or disables constrained VBR mode.
func (e *Encoder) SetConstrainedVBR(enabled bool) {
	e.constrainedVBR = enabled
}

// ConstrainedVBR reports whether constrained VBR mode is enabled.
func (e *Encoder) ConstrainedVBR() bool {
	return e.constrainedVBR
}

// SetConstrainedVBRBoundScale sets a scale for constrained-VBR vbr_bound.
// Valid range is [0, 1], where 1 matches libopus single-stream behavior.
func (e *Encoder) SetConstrainedVBRBoundScale(scale float32) {
	if scale < 0 {
		scale = 0
	} else if scale > 1 {
		scale = 1
	}
	e.constrainedVBRBoundScale = scale
}

// SetPrediction controls CELT inter-frame prediction behavior.
// Valid modes mirror libopus CELT_SET_PREDICTION:
// - 0: disable prediction and force intra (disable_pf=1, force_intra=1)
// - 1: disable prefilter only (disable_pf=1, force_intra=0)
// - 2: normal prediction (disable_pf=0, force_intra=0)
func (e *Encoder) SetPrediction(mode int) {
	if mode < 0 {
		mode = 0
	}
	if mode > 2 {
		mode = 2
	}
	e.disablePrefilter = mode <= 1
	e.forceIntra = mode == 0
}

// Prediction returns the active CELT prediction mode (0, 1, or 2).
func (e *Encoder) Prediction() int {
	if e.forceIntra {
		return 0
	}
	if e.disablePrefilter {
		return 1
	}
	return 2
}

// SetDCRejectEnabled controls whether EncodeFrame applies dc_reject().
// For Opus-level encoding, this should be false because dc_reject is already applied.
func (e *Encoder) SetDCRejectEnabled(enabled bool) {
	e.dcRejectEnabled = enabled
}

// DCRejectEnabled reports whether dc_reject is applied in EncodeFrame.
func (e *Encoder) DCRejectEnabled() bool {
	return e.dcRejectEnabled
}

// SetLSBQuantizationEnabled controls whether EncodeFrame rounds inputs to the
// configured LSB depth before CELT-local processing.
func (e *Encoder) SetLSBQuantizationEnabled(enabled bool) {
	e.lsbQuantizationEnabled = enabled
}

// LSBQuantizationEnabled reports whether EncodeFrame applies the CELT-local
// LSB-depth rounding step.
func (e *Encoder) LSBQuantizationEnabled() bool {
	return e.lsbQuantizationEnabled
}

// SetDelayCompensationEnabled controls whether EncodeFrame prepends Fs/250
// lookahead history before CELT analysis/quantization.
func (e *Encoder) SetDelayCompensationEnabled(enabled bool) {
	e.delayCompensationEnabled = enabled
}

// DelayCompensationEnabled reports whether EncodeFrame applies lookahead
// delay compensation.
func (e *Encoder) DelayCompensationEnabled() bool {
	return e.delayCompensationEnabled
}

// SetTopLevelDelayCompensatedInput tells the standalone CELT core whether the
// Opus wrapper has already supplied delay-compensated input.
func (e *Encoder) SetTopLevelDelayCompensatedInput(alreadyCompensated bool) {
	e.delayCompensationEnabled = !alreadyCompensated
}

// Complexity returns the current complexity setting.
func (e *Encoder) Complexity() int {
	return int(e.complexity)
}

// SetRangeEncoder sets the range encoder for the current frame.
// This must be called before encoding each frame.
func (e *Encoder) SetRangeEncoder(re *rangecoding.Encoder) {
	e.rangeEncoder = re
}

// RangeEncoder returns the current range encoder.
func (e *Encoder) RangeEncoder() *rangecoding.Encoder {
	return e.rangeEncoder
}

// Channels returns the number of audio channels (1 or 2).
func (e *Encoder) Channels() int {
	return int(e.channels)
}

// SetStreamChannels mirrors libopus CELT_SET_CHANNELS.
func (e *Encoder) SetStreamChannels(channels int) {
	if channels < 1 || channels > int(e.channels) {
		return
	}
	e.streamChannels = int32(channels)
}

// StreamChannels returns the coded channel count.
func (e *Encoder) StreamChannels() int {
	return int(e.streamChannels)
}

func (e *Encoder) codedChannels() int {
	channels := min(max(int(e.streamChannels), 1), int(e.channels))
	return channels
}

// SampleRate returns the operating sample rate (always 48000 for CELT).
func (e *Encoder) SampleRate() int {
	return int(e.sampleRate)
}

// PrevEnergy returns the previous frame's band energies.
// Used for inter-frame energy prediction in coarse energy encoding.
// Layout: [band0_ch0, band1_ch0, ..., band20_ch0, band0_ch1, ..., band20_ch1]
func (e *Encoder) PrevEnergy() []celtGLog {
	out := make([]celtGLog, len(e.prevEnergy))
	copy(out, e.prevEnergy)
	return out
}

// CopyPrevEnergyFloat32 copies the previous frame's band energies into dst as
// float32, reusing dst when its capacity is sufficient. The same layout as
// PrevEnergy is used.
func (e *Encoder) CopyPrevEnergyFloat32(dst []float32) []float32 {
	if cap(dst) < len(e.prevEnergy) {
		dst = make([]float32, len(e.prevEnergy))
	} else {
		dst = dst[:len(e.prevEnergy)]
	}
	copy(dst, e.prevEnergy)
	return dst
}

// PrevEnergy2 returns the band energies from two frames ago.
// Used for anti-collapse detection.
func (e *Encoder) PrevEnergy2() []celtGLog {
	out := make([]celtGLog, len(e.prevEnergy2))
	copy(out, e.prevEnergy2)
	return out
}

// SetPrevEnergy shifts current prev to prev2 and sets new prev energies.
// This should be called after encoding a frame with the actual energies used.
func (e *Encoder) SetPrevEnergy(energies []celtGLog) {
	// Shift: current prev becomes prev2
	copy(e.prevEnergy2, e.prevEnergy)
	// Copy new energies to prev
	copy(e.prevEnergy, energies)
}

// SetPrevEnergyWithPrev updates prevEnergy using the provided previous state.
// This avoids losing the prior frame when prevEnergy is updated during encoding.
func (e *Encoder) SetPrevEnergyWithPrev(prev, energies []celtGLog) {
	if len(prev) == len(e.prevEnergy2) {
		copy(e.prevEnergy2, prev)
	} else {
		copy(e.prevEnergy2, e.prevEnergy)
	}
	copy(e.prevEnergy, energies)
}

// SetPrevEnergyWithPrevFloat32 is the float32 form of SetPrevEnergyWithPrev: it
// sets the two-frames-ago energies from prev (falling back to the current
// prevEnergy when prev has the wrong length) and the previous-frame energies
// from energies.
func (e *Encoder) SetPrevEnergyWithPrevFloat32(prev, energies []float32) {
	if len(prev) == len(e.prevEnergy2) {
		copy(e.prevEnergy2, prev)
	} else {
		copy(e.prevEnergy2, e.prevEnergy)
	}
	copy(e.prevEnergy, energies)
}

func (e *Encoder) setPrevEnergyWithPrevGLog(prev, energies []celtGLog) {
	if len(prev) == len(e.prevEnergy2) {
		copy(e.prevEnergy2, prev)
	} else {
		copy(e.prevEnergy2, e.prevEnergy)
	}
	copy(e.prevEnergy, energies)
}

// OverlapBuffer returns the overlap buffer for MDCT analysis.
// Size is Overlap * channels samples.
func (e *Encoder) OverlapBuffer() []float32 {
	out := make([]float32, len(e.overlapBuffer))
	copySigToFloat32(out, e.overlapBuffer)
	return out
}

// OverlapBufferInto copies the overlap buffer into dst as float32 samples and
// returns the number of samples written. It performs the same conversion as
// OverlapBuffer without allocating, for the hot multi-frame encode path.
func (e *Encoder) OverlapBufferInto(dst []float32) int {
	n := min(len(e.overlapBuffer), len(dst))
	copySigToFloat32(dst[:n], e.overlapBuffer[:n])
	return n
}

// SetOverlapBuffer copies the given samples to the overlap buffer.
func (e *Encoder) SetOverlapBuffer(samples []float32) {
	copyFloat32ToSig(e.overlapBuffer, samples)
}

// PreemphState returns the pre-emphasis filter state.
// One value per channel.
func (e *Encoder) PreemphState() []float32 {
	return e.preemphState
}

// RNG returns the current RNG state.
// After encoding, this contains the final range coder state for verification.
func (e *Encoder) RNG() uint32 {
	return e.rng
}

// FinalRange returns the final range coder state after encoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after EncodeFrame() to get a meaningful value.
func (e *Encoder) FinalRange() uint32 {
	return e.rng
}

// SetRNG sets the RNG state.
func (e *Encoder) SetRNG(seed uint32) {
	e.rng = seed
}

// NextRNG advances the RNG and returns the new value.
// Uses the same LCG as libopus for deterministic behavior (D03-04-03).
func (e *Encoder) NextRNG() uint32 {
	e.rng = e.rng*1664525 + 1013904223
	return e.rng
}

// GetEnergy returns the energy for a specific band and channel from prevEnergy.
func (e *Encoder) GetEnergy(band, channel int) float32 {
	stride := e.predStride()
	if band < 0 || band >= stride || channel < 0 || channel >= int(e.channels) {
		return 0
	}
	return float32(e.prevEnergy[channel*stride+band])
}

// SetEnergy sets the energy for a specific band and channel.
func (e *Encoder) SetEnergy(band, channel int, energy float32) {
	stride := e.predStride()
	if band < 0 || band >= stride || channel < 0 || channel >= int(e.channels) {
		return
	}
	e.prevEnergy[channel*stride+band] = celtGLog(energy)
}

// IsIntraFrame returns a conservative pre-encode advisory value.
//
// The actual intra/inter decision is made inside EncodeFrame once band energies
// are available, so this helper is intentionally not a packet-exact predictor
// of the libopus-style two-pass coarse-energy search.
func (e *Encoder) IsIntraFrame() bool {
	return false
}

// IncrementFrameCount increments the frame counter.
// Call this after successfully encoding a frame.
func (e *Encoder) IncrementFrameCount() {
	e.frameCount++
}

// FrameCount returns the number of frames encoded.
func (e *Encoder) FrameCount() int {
	return int(e.frameCount)
}

// SetBitrate sets the target bitrate in bits per second.
// This affects bit allocation for frame encoding.
func (e *Encoder) SetBitrate(bps int) {
	e.targetBitrate = int32(bps)
}

// Bitrate returns the current target bitrate in bits per second.
func (e *Encoder) Bitrate() int {
	return int(e.targetBitrate)
}

// SetMaxPayloadBytes sets an optional payload cap for the next CELT frame.
// The value excludes the Opus TOC byte. A value <= 0 disables the cap.
func (e *Encoder) SetMaxPayloadBytes(maxPayloadBytes int) {
	if maxPayloadBytes < 0 {
		maxPayloadBytes = 0
	}
	e.maxPayloadBytes = int32(maxPayloadBytes)
}

// SetPacketLoss sets the expected packet loss percentage (0-100).
// This affects the prefilter gain for improved loss resilience.
func (e *Encoder) SetPacketLoss(lossPercent int) {
	if lossPercent < 0 {
		lossPercent = 0
	}
	if lossPercent > 100 {
		lossPercent = 100
	}
	e.packetLoss = int32(lossPercent)
}

// PacketLoss returns the expected packet loss percentage.
func (e *Encoder) PacketLoss() int {
	return int(e.packetLoss)
}

// SetLSBDepth sets the input signal LSB depth (8-24 bits).
// This affects masking/spread decisions at low bitrates.
func (e *Encoder) SetLSBDepth(depth int) {
	if depth < 8 {
		depth = 8
	}
	if depth > 24 {
		depth = 24
	}
	e.lsbDepth = int32(depth)
}

// LSBDepth returns the current input signal LSB depth.
func (e *Encoder) LSBDepth() int {
	if e.lsbDepth <= 0 {
		return 24
	}
	return int(e.lsbDepth)
}

// SetBandwidth sets the CELT bandwidth cap used for band allocation.
func (e *Encoder) SetBandwidth(bw CELTBandwidth) {
	if bw < CELTNarrowband || bw > CELTFullband {
		bw = CELTFullband
	}
	e.bandwidth = bw
}

// SetUpsample sets the CELT input upsample factor, mirroring libopus
// CELTEncoder.upsample = resampling_factor(Fs). At sub-48 kHz API rates the
// encoder consumes native-Fs frame sizes and the CELT core block is
// frameSize*upsample (the input is zero-stuffed in pre-emphasis). Factor 1 (or
// 0) is the 48 kHz path.
func (e *Encoder) SetUpsample(factor int) {
	if factor < 1 {
		factor = 1
	}
	e.upsample = int32(factor)
}

// effectiveUpsample returns the CELT input upsample factor (>=1).
func (e *Encoder) effectiveUpsample() int {
	if e.upsample <= 0 {
		return 1
	}
	return int(e.upsample)
}

// Bandwidth returns the active CELT bandwidth cap.
func (e *Encoder) Bandwidth() CELTBandwidth {
	return e.bandwidth
}

// scaleBase returns the short-MDCT base used to scale band-bin edges. It is
// Overlap (120) for the 48 kHz modes and the custom mode's short-MDCT size for
// the Fs==400*shortMdctSize family. The default build leaves customScaleBase at
// zero, so this is a constant Overlap (zero-cost).
func (e *Encoder) scaleBase() int {
	if e.customScaleBase > 0 {
		return e.customScaleBase
	}
	return Overlap
}

// modeConfig returns the frame-size-dependent ModeConfig for the active mode.
// For a custom mode in the Fs==400*shortMdctSize family it derives LM from the
// short-block decomposition (frameSize/customScaleBase) rather than the 48 kHz
// grid, so 20 ms family frames (e.g. 24000/480) get LM=3/ShortBlocks=8 like
// libopus instead of the 48 kHz LM=2.
func (e *Encoder) modeConfig(frameSize int) ModeConfig {
	if e.customScaleBase > 0 {
		nbShort := frameSize / e.customScaleBase
		lm := 0
		for (1 << lm) < nbShort {
			lm++
		}
		eff := MaxBands
		if e.customEffBands > 0 {
			eff = e.customEffBands
		}
		return ModeConfig{
			FrameSize:   frameSize,
			ShortBlocks: nbShort,
			LM:          lm,
			EffBands:    eff,
			MDCTSize:    frameSize,
		}
	}
	return GetModeConfig(frameSize)
}

// toneDetectFs returns the sample rate tone_detect()/transient_analysis() must
// use (libopus mode->Fs): 48000 for the standard 48 kHz modes, and the custom
// mode's Fs for the Fs==400*shortMdctSize family (which sets maxDelay=Fs/3000
// and the tone-frequency normalisation). The default build keeps e.sampleRate
// at 48000, so this is a constant 48000.
func (e *Encoder) toneDetectFs() int {
	if e.customScaleBase > 0 && e.sampleRate > 0 {
		return int(e.sampleRate)
	}
	return 48000
}

// validFrameSize reports whether frameSize is acceptable for the active mode.
func (e *Encoder) validFrameSize(frameSize int) bool {
	if e.customScaleBase > 0 {
		return frameSize > 0 && frameSize%e.customScaleBase == 0
	}
	return ValidFrameSize(frameSize)
}

func (e *Encoder) effectiveBandCount(frameSize int) int {
	nbBands := e.modeConfig(frameSize).EffBands
	if e.customEffBands > 0 && e.customEffBands < nbBands {
		nbBands = e.customEffBands
	}
	bwBands := EffectiveBandsForFrameSize(e.bandwidth, frameSize)
	if bwBands < nbBands {
		nbBands = bwBands
	}
	if nbBands < 1 {
		nbBands = 1
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	return nbBands
}

// TapsetDecision returns the current tapset decision (0, 1, or 2).
// The tapset controls the window taper used in the prefilter/postfilter comb filter:
// - 0: Narrow taper (concentrated energy)
// - 1: Medium taper (balanced)
// - 2: Wide taper (spread energy)
// This is computed during SpreadingDecision when updateHF=true.
// Reference: libopus celt/bands.c spreading_decision() and celt/celt.c comb_filter()
func (e *Encoder) TapsetDecision() int {
	return int(e.tapsetDecision)
}

// SetTapsetDecision sets the tapset decision value.
// Valid values are 0, 1, or 2.
func (e *Encoder) SetTapsetDecision(tapset int) {
	if tapset < 0 {
		tapset = 0
	}
	if tapset > 2 {
		tapset = 2
	}
	e.tapsetDecision = int32(tapset)
}

// HFAverage returns the high-frequency average used for tapset decision.
// This is updated during SpreadingDecision when updateHF=true.
func (e *Encoder) HFAverage() int {
	return int(e.hfAverage)
}

// SetHybrid sets the hybrid mode flag.
// When true, postfilter flag encoding is skipped per RFC 6716 Section 3.2.
// Reference: libopus celt_encoder.c line 2047-2048:
//
//	if(!hybrid && tell+16<=total_bits) ec_enc_bit_logp(enc, 0, 1);
func (e *Encoder) SetHybrid(hybrid bool) {
	e.hybrid = hybrid
}

// IsHybrid returns true if the encoder is in hybrid mode.
func (e *Encoder) IsHybrid() bool {
	return e.hybrid
}

// SetSilkInfo stores the current SILK signal classification for hybrid CELT.
// This mirrors libopus CELT_SET_SILK_INFO.
func (e *Encoder) SetSilkInfo(signalType, offset int) {
	e.silkSignalType = signalType
	e.silkOffset = offset
}

// FillHybridTFResolution applies the libopus hybrid fixed-TF fallback used when
// variable TF analysis is disabled.
func FillHybridTFResolution(tfRes []int32, end int, transient, weakTransient, allowWeakTransients bool) int {
	if end > len(tfRes) {
		end = len(tfRes)
	}
	if end < 0 {
		end = 0
	}
	tfSelect := 0
	switch {
	case weakTransient:
		for i := 0; i < end; i++ {
			tfRes[i] = 1
		}
	case allowWeakTransients:
		for i := 0; i < end; i++ {
			tfRes[i] = 0
		}
		if transient {
			tfSelect = 1
		}
	default:
		value := 0
		if transient {
			value = 1
		}
		for i := 0; i < end; i++ {
			tfRes[i] = int32(value)
		}
	}
	return tfSelect
}

// SetLFE enables or disables LFE mode constraints.
func (e *Encoder) SetLFE(enabled bool) {
	e.lfe = enabled
}

// LFE reports whether LFE mode constraints are enabled.
func (e *Encoder) LFE() bool {
	return e.lfe
}

// LastTonality returns the most recently computed tonality estimate.
// The value ranges from 0 (noise-like spectrum) to 1 (pure tone).
// This is used by computeVBRTarget for bit allocation decisions.
func (e *Encoder) LastTonality() opusVal16 {
	return e.lastTonality
}

// SetLastTonality sets the tonality estimate (for testing or manual override).
// Valid range is [0, 1] where 0 = noise and 1 = pure tone.
func (e *Encoder) SetLastTonality(tonality opusVal16) {
	if tonality < 0 {
		tonality = 0
	}
	if tonality > 1 {
		tonality = 1
	}
	e.lastTonality = tonality
}

// PrevBandLogEnergy returns the previous frame's band log-energies.
// Used for spectral flux computation in tonality analysis.
func (e *Encoder) PrevBandLogEnergy() []celtGLog {
	out := make([]celtGLog, len(e.prevBandLogEnergy))
	copy(out, e.prevBandLogEnergy)
	return out
}

// GetLastDynalloc returns the last computed dynalloc result.
// This is computed during encoding and stored for the next frame's VBR decisions.
func (e *Encoder) GetLastDynalloc() DynallocResult {
	return e.lastDynalloc
}

// GetLastBandLogE returns the last frame's primary band log-energies.
// These are the bandLogE values passed to DynallocAnalysis.
func (e *Encoder) GetLastBandLogE() []celtGLog {
	out := make([]celtGLog, len(e.lastBandLogE))
	copy(out, e.lastBandLogE)
	return out
}

// GetLastBandLogE2 returns the last frame's secondary band log-energies.
// For transients, this is from the long MDCT; otherwise same as bandLogE.
func (e *Encoder) GetLastBandLogE2() []celtGLog {
	out := make([]celtGLog, len(e.lastBandLogE2))
	copy(out, e.lastBandLogE2)
	return out
}

// SetPhaseInversionDisabled disables stereo phase inversion.
// When true, the encoder will not use phase inversion for stereo decorrelation.
// This can improve compatibility with some audio processing chains.
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	e.phaseInversionDisabled = disabled
}

// PhaseInversionDisabled returns whether stereo phase inversion is disabled.
func (e *Encoder) PhaseInversionDisabled() bool {
	return e.phaseInversionDisabled
}

// encoderScratch holds pre-allocated scratch buffers for the encoder hot path.
// These buffers are reused across frames to eliminate heap allocations during encoding.
type encoderScratch struct {
	// f32 backs the frameSize-dependent float-family scratch (the many []float32 /
	// []celtSig / []celtGLog / []celtEner / []celtNorm fields below) with one
	// contiguous allocation instead of ~40 separate ones. See ensureEncodeFloatArena;
	// the per-field ensure* sizing lines reslice/clear within their cap-pinned slot.
	f32 arena.Bump[float32]

	// LSB-depth quantized input buffer
	quantizedInputF32 []float32

	// DC rejection output buffer
	dcRejectedF32 []float32

	// Combined delay buffer + PCM
	combinedBufF32 []float32

	// Pre-emphasized signal buffer
	preemph []float32

	// Sub-48 kHz API-rate zero-stuffed core input (frameSize*upsample).
	upsampleStuff []float32

	// Transient analysis input buffer (overlap + frame)
	transientInput []float32

	// Prefilter (comb filter) scratch buffers
	prefilterPre      []celtSig
	prefilterOut      []celtSig
	prefilterPitchBuf []float32
	prefilterXLP4     []float32
	prefilterYLP4     []float32
	prefilterXcorr    []float32
	prefilterYYLookup []float32

	// MDCT coefficient buffers
	mdctCoeffsF32 []float32
	mdctLeftF32   []float32
	mdctRightF32  []float32

	// Band energy buffers
	energies  []celtGLog
	bandLogE2 []celtGLog
	bandE     []celtEner
	bandEL    []celtEner
	bandER    []celtEner

	// History buffers for MDCT
	leftHist  []float32
	rightHist []float32

	// MDCT overlap-history snapshots (sized to the active analysis overlap:
	// Overlap at 48 kHz, 240 in the native 96 kHz HD mode).
	mdctPrevL []float32
	mdctPrevR []float32

	// Range encoder buffer
	reBuf []byte

	// Quantized energies
	quantizedEnergies []celtGLog
	coarseError       []celtGLog
	coarseDecisionE   []celtGLog
	analysisEnergies  []celtGLog
	silenceEnergyVBR  []celtGLog
	silenceFreqVBR    []float32
	prev1LogE         []celtGLog

	// Normalized coefficient buffers
	normL []celtNorm
	normR []celtNorm
	// Interleaved stereo normalized coefficients for spread analysis.
	normStereo []celtNorm

	// Allocation-related buffers
	caps    []int32
	offsets []int32
	logN    []int16

	// TF analysis buffers
	tfRes []int32

	// PVQ search buffers
	pvqSignx []byte
	pvqY     []float32
	pvqAbsX  []float32
	pvqIy    []int32

	// Deinterleave buffers
	deintLeft  []float32
	deintRight []float32

	// MDCT forward transform scratch (float32)
	mdctF           []float32
	mdctFFTIn       []complex64
	mdctFFTOut      []complex64
	mdctFFTTmp      []kissCpx
	mdctBlockCoeffs []float32 // Per-block coefficients for short MDCT

	// Transient analysis scratch
	transientEnergy    []float32
	transientEnergyR   []float32
	transientX         []float32
	transientSpreadOld []celtGLog

	// CWRS encoding scratch
	cwrsU []uint32

	// ComputeAllocation scratch
	allocBits         []int32
	allocFineBits     []int32
	allocFinePrio     []int32
	allocThresh       []int32
	allocTrim         []int32
	allocTrimNormL    []celtNorm
	allocTrimNormR    []celtNorm
	allocTrimBandLogE []celtGLog
	allocCaps         []int32
	allocResult       AllocationResult // Pre-allocated result struct
	encoderQEXTScratchFields

	// MDCT input buffer for ComputeMDCTWithHistory
	mdctInput []float32

	// Band encode scratch (for quantAllBandsEncode)
	bandEncode bandEncodeScratch

	// Range encoder (reused between frames)
	rangeEncoder rangecoding.Encoder

	// Coarse-energy two-pass scratch
	coarseStartState rangecoding.EncoderState
	coarseOldStart   []celtGLog

	// Per-mode allocation work buffer (non-standard custom modes only).
	allocWork []int32
}

// allocationScratch returns the per-mode allocation work buffer, sized for the
// active band count. Used only by the non-standard custom-mode encode path.
func (e *Encoder) allocationScratch() []int32 {
	nb := MaxBands
	if e.perMode != nil {
		nb = e.perMode.nbEBands
	}
	return ensureInt32Slice(&e.scratch.allocWork, nb*4)
}

// combScale returns the comb-filter period scale for the active mode. It is
// QEXT_SCALE (2) at the native 96 kHz HD mode and 1 otherwise. In the default
// build hd96kOverlap is always 0, so this is a constant 1 (zero-cost).
//
// C ref: celt_encoder.c run_prefilter() max_period = QEXT_SCALE(COMBFILTER_MAXPERIOD).
func (e *Encoder) combScale() int {
	if e.hd96kOverlap > 0 && e.sampleRate == 96000 {
		return 2
	}
	return 1
}

// combMaxPeriod returns the comb-filter max period for the active mode
// (QEXT_SCALE(COMBFILTER_MAXPERIOD)).
func (e *Encoder) combMaxPeriod() int { return combFilterMaxPeriod * e.combScale() }

// combMinPeriod returns the comb-filter min period for the active mode
// (QEXT_SCALE(COMBFILTER_MINPERIOD)).
func (e *Encoder) combMinPeriod() int { return combFilterMinPeriod * e.combScale() }

// EnsureScratch ensures all scratch buffers are properly sized for the given frame size.
// Call this before using the encoder's scratch-aware methods from an external path
// (e.g., hybrid encoding) that does not go through EncodeFrame.
func (e *Encoder) EnsureScratch(frameSize int) {
	e.ensureScratch(frameSize)
}

// ensureEncodeFloatArena backs the frameSize-dependent float-family scratch with
// one contiguous arena, carving a cap-pinned slot per field. The carved length is
// 0; the per-field ensure* sizing lines in ensureScratch then set each field's
// visible length and clearing within its own slot. Re-carves only when the
// frame's total exceeds the backing capacity (i.e. a larger frameSize than seen
// before); otherwise it is a cheap early return. Layout only — bit-exact.
func (s *encoderScratch) ensureEncodeFloatArena(frameSize, channels, overlap, maxPeriod, maxPitch int) {
	expectedLen := frameSize * channels
	combinedLen := DelayCompensation*channels + expectedLen
	transientLen := (overlap + frameSize) * channels
	prefilterLen := (maxPeriod + frameSize) * channels
	pitchBufLen := max((maxPeriod+frameSize)>>1, 1)
	xcorrLen := maxPitch >> 1
	xlp4Len := max(frameSize>>2, 1)
	ylp4Len := max((frameSize+maxPitch)>>2, 1)
	yyLookupLen := max((maxPeriod>>1)+1, 1)
	bandCount := MaxBands * channels
	sp2 := (frameSize + overlap) / 2
	spc := frameSize + overlap
	const maxPVQN = maxBandWidth * 2

	total := expectedLen*3 + combinedLen + transientLen + prefilterLen*2 +
		pitchBufLen + xcorrLen + xlp4Len + ylp4Len + yyLookupLen +
		frameSize*2 + frameSize*2 + bandCount*8 + MaxBands*2 + overlap*2 +
		frameSize*2 + frameSize*2 + frameSize*2 + frameSize + frameSize/2 +
		sp2*2 + spc + frameSize*2 + spc + maxPVQN*2
	if s.f32.Cap() >= total {
		return
	}
	s.f32.Ensure(total)
	s.quantizedInputF32 = s.f32.Alloc(expectedLen)
	s.dcRejectedF32 = s.f32.Alloc(expectedLen)
	s.preemph = s.f32.Alloc(expectedLen)
	s.combinedBufF32 = s.f32.Alloc(combinedLen)
	s.transientInput = s.f32.Alloc(transientLen)
	s.prefilterPre = s.f32.Alloc(prefilterLen)
	s.prefilterOut = s.f32.Alloc(prefilterLen)
	s.prefilterPitchBuf = s.f32.Alloc(pitchBufLen)
	s.prefilterXcorr = s.f32.Alloc(xcorrLen)
	s.prefilterXLP4 = s.f32.Alloc(xlp4Len)
	s.prefilterYLP4 = s.f32.Alloc(ylp4Len)
	s.prefilterYYLookup = s.f32.Alloc(yyLookupLen)
	s.mdctCoeffsF32 = s.f32.Alloc(frameSize * 2)
	s.mdctLeftF32 = s.f32.Alloc(frameSize)
	s.mdctRightF32 = s.f32.Alloc(frameSize)
	s.energies = s.f32.Alloc(bandCount)
	s.bandLogE2 = s.f32.Alloc(bandCount)
	s.bandE = s.f32.Alloc(bandCount)
	s.coarseError = s.f32.Alloc(bandCount)
	s.quantizedEnergies = s.f32.Alloc(bandCount)
	s.prev1LogE = s.f32.Alloc(bandCount)
	s.coarseDecisionE = s.f32.Alloc(bandCount)
	s.allocTrimBandLogE = s.f32.Alloc(bandCount)
	s.bandEL = s.f32.Alloc(MaxBands)
	s.bandER = s.f32.Alloc(MaxBands)
	s.leftHist = s.f32.Alloc(overlap)
	s.rightHist = s.f32.Alloc(overlap)
	s.normL = s.f32.Alloc(frameSize)
	s.normR = s.f32.Alloc(frameSize)
	s.normStereo = s.f32.Alloc(frameSize * 2)
	s.deintLeft = s.f32.Alloc(frameSize)
	s.deintRight = s.f32.Alloc(frameSize)
	s.mdctF = s.f32.Alloc(frameSize)
	s.mdctBlockCoeffs = s.f32.Alloc(frameSize / 2)
	s.transientEnergy = s.f32.Alloc(sp2)
	s.transientEnergyR = s.f32.Alloc(sp2)
	s.transientX = s.f32.Alloc(spc)
	s.allocTrimNormL = s.f32.Alloc(frameSize)
	s.allocTrimNormR = s.f32.Alloc(frameSize)
	s.mdctInput = s.f32.Alloc(spc)
	s.pvqY = s.f32.Alloc(maxPVQN)
	s.pvqAbsX = s.f32.Alloc(maxPVQN)
}

// ensureScratch ensures all scratch buffers are properly sized for the given frame parameters.
// Call this at the start of EncodeFrame to prepare buffers for reuse.
func (e *Encoder) ensureScratch(frameSize int) {
	channels := int(e.channels)
	expectedLen := frameSize * channels
	overlap := min(e.analysisOverlap(), frameSize)

	s := &e.scratch

	// Carve the frameSize-dependent float-family scratch from one contiguous
	// arena first; the per-field sizing below reslices/clears within each slot.
	maxPeriod := max(e.combMaxPeriod(), e.combMinPeriod())
	maxPitch := max(maxPeriod-3*e.combMinPeriod(), 1)
	s.ensureEncodeFloatArena(frameSize, channels, overlap, maxPeriod, maxPitch)

	// DC rejection and LSB-depth quantization output
	s.quantizedInputF32 = ensureFloat32Slice(&s.quantizedInputF32, expectedLen)
	s.dcRejectedF32 = ensureFloat32Slice(&s.dcRejectedF32, expectedLen)

	// Combined delay buffer
	delayComp := DelayCompensation * channels
	combinedLen := delayComp + expectedLen
	s.combinedBufF32 = ensureFloat32Slice(&s.combinedBufF32, combinedLen)

	// Pre-emphasis buffer
	s.preemph = ensureFloat32Slice(&s.preemph, expectedLen)

	// Transient analysis input (overlap + frameSize) * channels
	transientLen := (overlap + frameSize) * channels
	s.transientInput = ensureFloat32Slice(&s.transientInput, transientLen)

	// Prefilter scratch buffers
	prefilterLen := (maxPeriod + frameSize) * channels
	s.prefilterPre = ensureSigSliceNoClear(&s.prefilterPre, prefilterLen)
	s.prefilterOut = ensureSigSliceNoClear(&s.prefilterOut, prefilterLen)
	pitchBufLen := max((maxPeriod+frameSize)>>1, 1)
	s.prefilterPitchBuf = ensureFloat32Slice(&s.prefilterPitchBuf, pitchBufLen)
	s.prefilterXcorr = ensureFloat32Slice(&s.prefilterXcorr, maxPitch>>1)
	xlp4Len := max(frameSize>>2, 1)
	s.prefilterXLP4 = ensureFloat32Slice(&s.prefilterXLP4, xlp4Len)
	lag := frameSize + maxPitch
	ylp4Len := max(lag>>2, 1)
	s.prefilterYLP4 = ensureFloat32Slice(&s.prefilterYLP4, ylp4Len)
	yyLookupLen := max((maxPeriod>>1)+1, 1)
	s.prefilterYYLookup = ensureFloat32Slice(&s.prefilterYYLookup, yyLookupLen)

	// MDCT coefficients
	s.mdctCoeffsF32 = ensureFloat32Slice(&s.mdctCoeffsF32, frameSize*2)
	s.mdctLeftF32 = ensureFloat32Slice(&s.mdctLeftF32, frameSize)
	s.mdctRightF32 = ensureFloat32Slice(&s.mdctRightF32, frameSize)

	// Band energies
	bandCount := MaxBands * channels
	s.energies = ensureGLogSlice(&s.energies, bandCount)
	s.bandLogE2 = ensureGLogSlice(&s.bandLogE2, bandCount)
	s.bandE = ensureEnerSlice(&s.bandE, bandCount)
	s.coarseError = ensureGLogSlice(&s.coarseError, bandCount)
	s.bandEL = ensureEnerSlice(&s.bandEL, MaxBands)
	s.bandER = ensureEnerSlice(&s.bandER, MaxBands)

	// History buffers
	s.leftHist = ensureFloat32Slice(&s.leftHist, overlap)
	s.rightHist = ensureFloat32Slice(&s.rightHist, overlap)

	// Range encoder buffer
	bufSize := 256
	if len(s.reBuf) < bufSize {
		s.reBuf = make([]byte, bufSize)
	}
	if extsupport.QEXT && e.qextActive() {
		qs := s.ensureQEXTScratch()
		qs.buf = ensureByteSlice(&qs.buf, qextPacketSizeCap)
	}

	// Quantized energies
	s.quantizedEnergies = ensureGLogSlice(&s.quantizedEnergies, bandCount)
	s.prev1LogE = ensureGLogSlice(&s.prev1LogE, bandCount)
	s.coarseDecisionE = ensureGLogSlice(&s.coarseDecisionE, bandCount)

	// Normalized coefficients
	s.normL = ensureNormSliceNoClear(&s.normL, frameSize)
	s.normR = ensureNormSliceNoClear(&s.normR, frameSize)
	s.normStereo = ensureNormSliceNoClear(&s.normStereo, frameSize*2)

	// Allocation buffers
	s.caps = ensureInt32Slice(&s.caps, MaxBands)
	s.offsets = ensureInt32Slice(&s.offsets, MaxBands)
	if len(s.logN) < MaxBands {
		s.logN = make([]int16, MaxBands)
	}
	s.allocBits = ensureInt32Slice(&s.allocBits, MaxBands)
	s.allocFineBits = ensureInt32Slice(&s.allocFineBits, MaxBands)
	s.allocFinePrio = ensureInt32Slice(&s.allocFinePrio, MaxBands)
	s.allocCaps = ensureInt32Slice(&s.allocCaps, MaxBands)
	// Initialize AllocationResult with pre-allocated slices
	s.allocResult.BandBits = s.allocBits
	s.allocResult.FineBits = s.allocFineBits
	s.allocResult.FinePriority = s.allocFinePrio
	s.allocResult.Caps = s.allocCaps

	// TF results
	s.tfRes = ensureInt32Slice(&s.tfRes, MaxBands)

	// Deinterleave buffers
	s.deintLeft = ensureFloat32Slice(&s.deintLeft, frameSize)
	s.deintRight = ensureFloat32Slice(&s.deintRight, frameSize)

	// MDCT forward transform scratch (float32)
	n4 := frameSize / 2 // n4 = frameSize/2 for N=2*frameSize MDCT
	s.mdctF = ensureFloat32Slice(&s.mdctF, frameSize)
	s.mdctFFTIn = ensureComplex64Slice(&s.mdctFFTIn, n4)
	s.mdctFFTOut = ensureComplex64Slice(&s.mdctFFTOut, n4)
	s.mdctFFTTmp = ensureKissCpxSlice(&s.mdctFFTTmp, n4)
	// For short MDCT: max short size is frameSize/8 (for 8 short blocks)
	s.mdctBlockCoeffs = ensureFloat32Slice(&s.mdctBlockCoeffs, frameSize/2)

	// Transient analysis scratch
	samplesPerChannel := frameSize + overlap
	s.transientEnergy = ensureFloat32Slice(&s.transientEnergy, samplesPerChannel/2)
	s.transientEnergyR = ensureFloat32Slice(&s.transientEnergyR, samplesPerChannel/2)
	s.transientX = ensureFloat32Slice(&s.transientX, samplesPerChannel)

	// CWRS encoding scratch (k can be up to ~128 for typical encoding)
	s.cwrsU = ensureUint32Slice(&s.cwrsU, 256)

	// ComputeAllocation scratch
	s.allocBits = ensureInt32Slice(&s.allocBits, MaxBands)
	s.allocFineBits = ensureInt32Slice(&s.allocFineBits, MaxBands)
	s.allocFinePrio = ensureInt32Slice(&s.allocFinePrio, MaxBands)
	s.allocThresh = ensureInt32Slice(&s.allocThresh, MaxBands)
	s.allocTrim = ensureInt32Slice(&s.allocTrim, MaxBands)
	s.allocTrimNormL = ensureNormSliceNoClear(&s.allocTrimNormL, frameSize)
	s.allocTrimNormR = ensureNormSliceNoClear(&s.allocTrimNormR, frameSize)
	s.allocTrimBandLogE = ensureGLogSlice(&s.allocTrimBandLogE, MaxBands*channels)
	if extsupport.QEXT && e.qextActive() {
		qs := s.ensureQEXTScratch()
		qs.extraBits = ensureInt32Slice(&qs.extraBits, MaxBands+nbQEXTBands)
		qs.fineBits = ensureInt32Slice(&qs.fineBits, MaxBands+nbQEXTBands)
		qs.bandE = ensureEnerSlice(&qs.bandE, nbQEXTBands*channels)
		qs.bandLogE = ensureGLogSlice(&qs.bandLogE, nbQEXTBands*channels)
		qs.quantized = ensureGLogSlice(&qs.quantized, nbQEXTBands*channels)
		qs.qerr = ensureGLogSlice(&qs.qerr, nbQEXTBands*channels)
		qs.oldBandE = ensureGLogSlice(&qs.oldBandE, MaxBands*channels)
		qs.normL = ensureNormSliceNoClear(&qs.normL, frameSize)
		qs.normR = ensureNormSliceNoClear(&qs.normR, frameSize)
	}

	// MDCT input buffer for ComputeMDCTWithHistory
	s.mdctInput = ensureFloat32Slice(&s.mdctInput, frameSize+overlap)

	// PVQ search buffers
	maxPVQN := maxBandWidth * 2 // Max band width with stereo doubling
	s.pvqSignx = ensureByteSlice(&s.pvqSignx, maxPVQN)
	s.pvqY = ensureFloat32Slice(&s.pvqY, maxPVQN)
	s.pvqAbsX = ensureFloat32Slice(&s.pvqAbsX, maxPVQN)
	s.pvqIy = ensureInt32Slice(&s.pvqIy, maxPVQN)

	// Band encode scratch. Carve the float-family fields (norm, lowbandScratch,
	// hadamardTmpNorm, pvqY/pvqAbsX/pvqX, theta-RDO slots) from one contiguous
	// arena first; the sizing/getters below reslice within their cap-pinned slots.
	s.bandEncode.ensureFloatScratch(channels)
	s.bandEncode.collapse = ensureByteSlice(&s.bandEncode.collapse, channels*MaxBands)
	normLen := 8 * EBands[MaxBands-1] // M=8 for 20ms frames
	s.bandEncode.norm = ensureNormSliceNoClear(&s.bandEncode.norm, channels*normLen)
	maxBand := 8 * (EBands[MaxBands] - EBands[MaxBands-1])
	s.bandEncode.lowbandScratch = ensureNormSliceNoClear(&s.bandEncode.lowbandScratch, maxBand)
	s.bandEncode.pvqSignx = ensureByteSlice(&s.bandEncode.pvqSignx, maxPVQN)
	s.bandEncode.pvqY = ensureFloat32Slice(&s.bandEncode.pvqY, maxPVQN)
	s.bandEncode.pvqAbsX = ensureFloat32Slice(&s.bandEncode.pvqAbsX, maxPVQN)
	s.bandEncode.pvqIy = ensureInt32Slice(&s.bandEncode.pvqIy, maxPVQN)
	s.bandEncode.qextIy = ensureInt32Slice(&s.bandEncode.qextIy, maxPVQN)
	s.bandEncode.cwrsU = ensureUint32Slice(&s.bandEncode.cwrsU, 256)
	s.bandEncode.hadamardTmpNorm = ensureNormSliceNoClear(&s.bandEncode.hadamardTmpNorm, maxBandWidth*16)
}

// computeAllocationScratch computes bit allocation using scratch buffers (zero-alloc).
// This is the zero-allocation version of ComputeAllocationWithEncoder.
func (e *Encoder) computeAllocationScratch(re *rangecoding.Encoder, totalBitsQ3, nbBands int, cap, offsets []int32, trim int, intensity int, dualStereo bool, lm int, prev int, signalBandwidth int) *AllocationResult {
	maxNb := MaxBands
	if e.perMode != nil {
		maxNb = e.perMode.nbEBands
	}
	if nbBands > maxNb {
		nbBands = maxNb
	}
	if nbBands < 0 {
		nbBands = 0
	}
	channels := min(max(e.codedChannels(), 1), 2)
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	// Use pre-allocated result from scratch
	result := &e.scratch.allocResult
	result.BandBits = e.scratch.allocBits[:nbBands]
	result.FineBits = e.scratch.allocFineBits[:nbBands]
	result.FinePriority = e.scratch.allocFinePrio[:nbBands]
	result.Caps = e.scratch.allocCaps[:nbBands]
	result.Balance = 0
	result.CodedBands = nbBands
	result.Intensity = 0
	result.DualStereo = false

	// Zero the slices
	for i := 0; i < nbBands; i++ {
		result.BandBits[i] = 0
		result.FineBits[i] = 0
		result.FinePriority[i] = 0
		result.Caps[i] = 0
	}

	if nbBands == 0 || totalBitsQ3 <= 0 {
		return result
	}

	if cap == nil || len(cap) < nbBands {
		if e.perMode != nil {
			cap = ensureInt32Slice(&e.scratch.allocCaps, nbBands)[:nbBands]
			initCapsIntoMode(cap, nbBands, lm, channels, e.perMode)
		} else {
			cap = initCaps(nbBands, lm, channels)
		}
	}
	copy(result.Caps, cap[:nbBands])

	if offsets == nil {
		offsets = e.scratch.offsets[:nbBands]
		for i := range offsets {
			offsets[i] = 0
		}
	}

	intensityVal := intensity
	dualVal := 0
	if dualStereo {
		dualVal = 1
	}
	balance := 0
	pulses := result.BandBits
	fineBits := result.FineBits
	finePriority := result.FinePriority

	var codedBands int
	if e.perMode != nil {
		codedBands = cltComputeAllocationWithScratchModeEncode(re, 0, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
			totalBitsQ3, &balance, pulses, fineBits, finePriority, channels, lm, prev, signalBandwidth, e.allocationScratch(), e.perMode)
	} else {
		codedBands = cltComputeAllocationEncode(re, 0, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
			totalBitsQ3, &balance, pulses, fineBits, finePriority, channels, lm, prev, signalBandwidth)
	}

	result.CodedBands = codedBands
	result.Balance = balance
	result.Intensity = intensityVal
	result.DualStereo = dualVal != 0

	return result
}

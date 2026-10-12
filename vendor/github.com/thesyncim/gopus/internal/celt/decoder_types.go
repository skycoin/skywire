package celt

import (
	"errors"

	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	frameNone = iota
	frameNormal
	framePLCNoise
	framePLCPeriodic
	framePLCNeural
	frameDRED
)

// Decoding errors
var (
	// ErrInvalidFrame indicates the frame data is invalid or corrupted.
	ErrInvalidFrame = errors.New("celt: invalid frame data")

	// ErrInvalidFrameSize indicates an invalid frame size.
	ErrInvalidFrameSize = errors.New("celt: invalid frame size")

	// ErrInvalidSampleRate indicates a sample rate outside the Opus API set.
	ErrInvalidSampleRate = errors.New("celt: invalid sample rate")

	// ErrOutputTooSmall indicates the caller-provided PCM buffer is too small.
	ErrOutputTooSmall = errors.New("celt: output buffer too small")

	// ErrNilDecoder indicates a nil range decoder was passed.
	ErrNilDecoder = errors.New("celt: nil range decoder")

	// ErrInvalidComplexity indicates the decoder complexity is out of range.
	ErrInvalidComplexity = errors.New("celt: invalid complexity (must be 0-10)")
)

// Decoder decodes CELT frame data and synthesizes PCM. It retains band-energy
// prediction, overlap, de-emphasis, postfilter, and loss history across frames.
// Its scratch and history are mutable, so use one decoder per stream and
// serialize access. See RFC 6716 Section 4.3.
type Decoder struct {
	// Configuration
	channels   int32 // libopus CELTDecoder.channels
	sampleRate int32 // Output sample rate (typically 48000)
	downsample int32 // libopus CELTDecoder.downsample, 1 at 48 kHz

	// Range decoder (set per frame)
	rangeDecoder *rangecoding.Decoder
	// rangeDecoderScratch holds a reusable decoder to avoid per-frame allocations.
	rangeDecoderScratch rangecoding.Decoder

	// Energy state (persists across frames for inter-frame prediction)
	prevEnergy []celtGLog // Previous frame band energies [MaxBands * channels]
	prevLogE   []celtGLog // Previous log energies (for anti-collapse history)
	prevLogE2  []celtGLog // Two frames ago log energies (for anti-collapse history)
	// Slow background floor estimate (libopus backgroundLogE cadence).
	backgroundEnergy []celtGLog

	// decodeMem is libopus CELTDecoder._decode_mem. Each channel reserves
	// 2*decodeMemLineLen() samples; its line is the decodeMemLineLen() window at
	// decodeMemOff within that reservation, and decode_mem[c] is the last
	// decodeMemLen() samples of the line (see decodeMemChannel).
	decodeMem    []celtSig
	decodeMemOff int
	preemphState []celtSig // De-emphasis filter state [channels]

	// Mode dimensions for synthesis and de-emphasis. Zero selects the standard
	// 48 kHz overlap and pre-emphasis coefficient. Native 96 kHz HD mode
	// (gopus_qext) sets synthOverlap=240 and deemphCoef to its pre-emphasis
	// coefficient.
	//
	// deemphCoef1/deemphCoef3 are the additional de-emphasis taps libopus uses
	// when mode->preemph[1] != 0 (the custom/QEXT 2-tap deemphasis path,
	// celt_decoder.c deemphasis()). They are zero for the 48 kHz mode, which
	// keeps the single-tap filter and leaves the 48 kHz path unchanged.
	synthOverlap int
	deemphCoef   float32
	deemphCoef1  float32
	deemphCoef3  float32

	// customScaleBase / customEffBands parameterize an Opus Custom mode in the
	// Fs==400*shortMdctSize family. Zero selects the standard 48 kHz band scale
	// and effective-band limit. When set the band-bin scale is
	// frameSize/customScaleBase == 1<<LM (libopus eBands[i]<<LM) and the decode
	// end band is customEffBands.
	customScaleBase int
	customEffBands  int
	// customFrameSize is the Opus Custom mode frame size (shortMdctSize *
	// nbShortMdcts), zero for the standard modes. It sizes the comb-filter
	// headroom before decode_mem (see decodeMemCombHeadroom).
	customFrameSize int

	// perMode carries band edges, widths, logN, allocation vectors, and pulse
	// cache for an Opus Custom mode whose layout differs from the static CELT
	// tables. The standard-table path leaves it nil; when non-nil, band decoding
	// uses these tables for energy stride, allocation, PVQ, and anti-collapse.
	perMode *perModeTables

	// Postfilter state (pitch-based comb filter)
	postfilterPeriod int32   // libopus CELTDecoder.postfilter_period
	postfilterGain   float32 // Comb filter gain
	postfilterTapset int32   // libopus CELTDecoder.postfilter_tapset
	// Previous postfilter state for overlap cross-fade
	postfilterPeriodOld int32
	postfilterGainOld   float32
	postfilterTapsetOld int32

	// Error recovery / deterministic randomness
	rng uint32 // RNG state for PLC and folding

	// Per-decoder PLC state (do not share across decoder instances).
	plcState *plc.State
	// CELT loss duration in libopus LM units (saturates at 10000).
	plcLossDuration int32
	// Mirrors libopus st->plc_duration for periodic/noise/DRED gating.
	plcDuration int32
	// Mirrors libopus st->last_frame_type.
	plcLastFrameType int32
	// Mirrors libopus st->skip_plc two-good-packets gate.
	plcSkip bool
	// Periodic PLC cadence state (mirrors libopus decode_lost() behavior).
	plcLastPitchPeriod     int32
	plcPrevLossWasPeriodic bool
	// Mirrors libopus prefilter_and_fold cadence after periodic PLC.
	plcPrefilterAndFoldPending bool
	// Stored LPC coefficients per channel for periodic PLC continuation.
	plcLPC []float32

	// Band processing state
	collapseMask uint32 // Tracks which bands received pulses (for anti-collapse)

	// Bandwidth (Opus TOC-derived)
	bandwidth              CELTBandwidth
	customEndBand          int32
	phaseInversionDisabled bool
	complexity             int32
	redundancyActive       bool
	redundancyBytes        []byte
	redundancyRange        uint32
	redundancyFrameSize    int

	// Channel transition tracking (for mono-to-stereo overlap buffer clearing)
	prevStreamChannels int32 // libopus CELTDecoder.stream_channels mirror (0 = uninitialized)
	// directOutPCM, when set, is the caller's PCM buffer that deemphasis writes
	// into (libopus celt_decode_with_ec's pcm argument). directOutAccum selects
	// libopus celt_accum: deemphasis adds its output onto the samples already in
	// directOutPCM (the SILK lowband in Hybrid mode) instead of overwriting them.
	directOutPCM   []float32
	directOutAccum bool
	// synthTrace, when non-nil, captures intermediate synthesis-stage buffers for
	// the next decoded frame (test-only; production decoders leave it nil so the
	// hot path is a single nil-pointer branch with no allocations).
	synthTrace *synthesisStageTrace
	// plcStageTrace, when non-nil, captures intermediate noise-PLC concealment
	// buffers for a target noise-PLC chunk (test-only; nil in production).
	plcStageTrace *plcStageTrace
	decoderQEXTFields

	// Scratch buffers to reduce per-frame allocations (decoder is not thread-safe).
	scratchPrevEnergyGLog   []celtGLog
	scratchEnergies         []celtGLog
	scratchStereoEnergies   []celtGLog
	scratchTFRes            []int32
	scratchOffsets          []int32
	scratchPulses           []int32
	scratchFineQuant        []int32
	scratchFinePriority     []int32
	scratchPrevBandEnergy   []float32
	scratchCaps             []int32
	stdAlloc                stdAllocState
	scratchAllocWork        []int32
	scratchBands            bandDecodeScratch
	scratchIMDCTF32         imdctScratchF32
	scratchIMDCTF32R        imdctScratchF32
	scratchSpecRF32         []float32
	scratchStereoF32        []float32
	scratchShortCoeffsF32   []float32
	scratchPCM              []float32
	scratchSynthF32         []float32
	scratchSynthRF32        []float32
	scratchPLCPitchHist     []celtSig
	scratchMonoToStereoRF32 []float32
	scratchMonoMixF32       []float32
	postfilterWindowSqF32   []float32
	postfilterWindowSqOf    *float32  // first element of the window postfilterWindowSqF32 squares
	scratchPLC              []float32 // Scratch buffer for PLC concealment samples
	scratchPLCPitchLP       []float32
	scratchPLCPitchSearch   plcPitchSearchScratch
	scratchPLCFIRTmp        []celtSig
	scratchPLCWindowed      []celtSig
	scratchPLCIIRY          []float32
	scratchPLCBuf           []celtSig
	scratchPLCExc           []celtSig
	decoderDREDState
	scratchPLCFoldDst     []celtSig
	scratchPLCHybridNormL []celtNorm
	scratchPLCHybridNormR []celtNorm
}

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

// Decoder decodes CELT frames from an Opus packet.
// It maintains state across frames for proper audio continuity via overlap-add
// synthesis and energy prediction.
//
// CELT is the transform-based layer of Opus, using the Modified Discrete Cosine
// Transform (MDCT) for music and general audio. The decoder reconstructs audio by:
// 1. Decoding energy envelope (coarse + fine quantization)
// 2. Decoding normalized band shapes via PVQ
// 3. Applying denormalization (scaling by energy)
// 4. Performing IMDCT synthesis with overlap-add
// 5. Applying de-emphasis filter
//
// Reference: RFC 6716 Section 4.3
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
	prevEnergy  []celtGLog // Previous frame band energies [MaxBands * channels]
	prevEnergy2 []celtGLog // Two frames ago energies (for anti-collapse)
	prevLogE    []celtGLog // Previous log energies (for anti-collapse history)
	prevLogE2   []celtGLog // Two frames ago log energies (for anti-collapse history)
	// Slow background floor estimate (libopus backgroundLogE cadence).
	backgroundEnergy []celtGLog

	// Synthesis state (persists for overlap-add)
	overlapBuffer []celtSig // Previous frame overlap tail [overlap * channels]
	preemphState  []celtSig // De-emphasis filter state [channels]

	// Mode dimensions for the synthesis/deemphasis tail. Zero selects the
	// 48 kHz fullband defaults (Overlap=120, PreemphCoef). The native 96 kHz
	// HD mode (gopus_qext) sets synthOverlap=240 and deemphCoef to the HD
	// preemphasis coefficient. Keeping them zero leaves the 48 kHz path
	// byte-identical to before this field existed.
	//
	// deemphCoef1/deemphCoef3 are the additional de-emphasis taps libopus uses
	// when mode->preemph[1] != 0 (the custom/QEXT 2-tap deemphasis path,
	// celt_decoder.c deemphasis()). They are zero for the 48 kHz mode, which
	// keeps the single-tap filter and leaves the 48 kHz path unchanged.
	synthOverlap int
	deemphCoef   float32
	deemphCoef1  float32
	deemphCoef3  float32

	// customScaleBase / customEffBands parameterize a non-standard Opus Custom
	// mode in the Fs==400*shortMdctSize family. Zero selects the 48 kHz base
	// (Overlap=120) and the standard effEBands clamp, keeping the default and
	// 48 kHz paths byte-identical. When set the band-bin scale is
	// frameSize/customScaleBase == 1<<LM (libopus eBands[i]<<LM) and the decode
	// end band is customEffBands.
	customScaleBase int
	customEffBands  int

	// perMode carries the per-mode CELT tables (band edges, widths, logN,
	// allocation matrix, pulse cache) for a non-standard Opus Custom mode whose
	// band layout differs from the static 21-band 48 kHz tables (e.g. 48000/640
	// with nbEBands=19). It is nil for the default build, the standard 48 kHz
	// modes, the Fs==400*shortMdctSize family (which reuse the 21-band tables)
	// and hybrid/QEXT, leaving every one of those paths byte-identical. When
	// non-nil the decode data plane (energy stride, allocation, band core,
	// anti-collapse, denormalisation) is driven by these tables.
	perMode *perModeTables

	// Postfilter state (pitch-based comb filter)
	postfilterPeriod int32   // libopus CELTDecoder.postfilter_period
	postfilterGain   float32 // Comb filter gain
	postfilterTapset int32   // libopus CELTDecoder.postfilter_tapset
	// Previous postfilter state for overlap cross-fade
	postfilterPeriodOld int32
	postfilterGainOld   float32
	postfilterTapsetOld int32
	// Postfilter history buffer (per-channel)
	postfilterMem []celtSig
	// On no-gain frames, postfilter history can be lazily reconstructed from
	// the longer PLC decode history, avoiding a duplicate history shift.
	postfilterMemFromPLC   bool
	postfilterMemPLCBacked bool
	// PLC decode history buffer (per-channel), sized to match libopus
	// DECODE_BUFFER_SIZE cadence used by celt_plc_pitch_search().
	plcDecodeMem []celtSig
	// Stereo planar decode keeps PLC history as a ring during good packets and
	// materializes it only before PLC consumers need contiguous libopus layout.
	plcDecodeMemRingActive bool
	plcDecodeMemRingStart  int

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
	phaseInversionDisabled bool
	complexity             int32
	redundancyActive       bool
	redundancyBytes        []byte
	redundancyRange        uint32
	redundancyFrameSize    int

	// Channel transition tracking (for mono-to-stereo overlap buffer clearing)
	prevStreamChannels int32 // libopus CELTDecoder.stream_channels mirror (0 = uninitialized)
	directOutPCM       []float32
	// synthTrace, when non-nil, captures intermediate synthesis-stage buffers for
	// the next decoded frame (test-only; production decoders leave it nil so the
	// hot path is a single nil-pointer branch with no allocations).
	synthTrace *synthesisStageTrace
	// plcStageTrace, when non-nil, captures intermediate noise-PLC concealment
	// buffers for a target noise-PLC chunk (test-only; nil in production).
	plcStageTrace *plcStageTrace
	decoderQEXTFields

	// Scratch buffers to reduce per-frame allocations (decoder is not thread-safe).
	scratchPrevEnergy       []celtGLog
	scratchPrevEnergyGLog   []celtGLog
	scratchEnergies         []celtGLog
	scratchTFRes            []int32
	scratchOffsets          []int32
	scratchPulses           []int32
	scratchFineQuant        []int32
	scratchFinePriority     []int32
	scratchPrevBandEnergy   []float32
	scratchSilenceE         []celtGLog
	scratchCaps             []int32
	scratchAllocWork        []int32
	scratchBands            bandDecodeScratch
	scratchIMDCTF32         imdctScratchF32
	scratchIMDCTF32R        imdctScratchF32
	scratchSynthF32         []float32
	scratchSilenceSpec      []float32
	scratchSynthRF32        []float32
	scratchSpecRF32         []float32
	scratchStereoF32        []float32
	scratchShortCoeffsF32   []float32
	scratchMonoToStereoRF32 []float32
	scratchMonoMixF32       []float32
	postfilterScratchF32    []float32
	postfilterWindowSqF32   []float32
	scratchPLC              []float32 // Scratch buffer for PLC concealment samples
	scratchPLCF32           []float32
	scratchPLCPitchLP       []float32
	scratchPLCPitchSearch   plcPitchSearchScratch
	scratchPLCFIRTmp        []celtSig
	scratchPLCWindowed      []celtSig
	scratchPLCIIRY          []float32
	scratchPLCBuf           []celtSig
	scratchPLCExc           []celtSig
	decoderDREDState
	scratchPLCFoldSrc     []celtSig
	scratchPLCFoldDst     []celtSig
	scratchPLCHybridNormL []celtNorm
	scratchPLCHybridNormR []celtNorm
}

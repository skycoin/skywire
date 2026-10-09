// Package encoder implements the stateful Opus encoder described by RFC 6716.
// It coordinates SILK, CELT, and Hybrid coding, selects a mode and bandwidth
// for each frame, applies bitrate control, and assembles the packet.
//
// A frame uses SILK for predictive speech coding, CELT for transform coding, or
// both in Hybrid mode. [ModeAuto] selects among them from the configured rate
// and the encoder's signal analysis. The frame path follows libopus 1.6.1's
// `src/opus_encoder.c` and `src/analysis.c`.
//
// [Encoder] retains analysis, sub-encoder, and transition history across calls.
// Keep one encoder per stream and serialize access. [Encoder.FinalRange] exposes the
// entropy coder's final range for paired comparisons; packet and range claims
// apply to the matching reference configuration and tested cases. See the
// coverage summary in `reports/validation.md#coverage`.
//
// Most applications should use the top-level gopus API, which owns this
// implementation state and packet framing.
package encoder

import (
	"errors"

	"github.com/thesyncim/gopus/internal/arena"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
)

// Mode represents the encoding mode.
type Mode int

const (
	// ModeAuto automatically selects the best mode based on content and bandwidth.
	ModeAuto Mode = iota
	// ModeSILK uses SILK-only encoding (configs 0-11).
	ModeSILK
	// ModeHybrid uses combined SILK+CELT encoding (configs 12-15).
	ModeHybrid
	// ModeCELT uses CELT-only encoding (configs 16-31).
	ModeCELT
)

// Errors for the encoder.
var (
	// ErrInvalidSampleRate indicates a sample rate outside the Opus API set.
	ErrInvalidSampleRate = errors.New("encoder: invalid sample rate (must be 8000, 12000, 16000, 24000, or 48000)")

	// ErrInvalidChannels indicates an invalid channel count.
	ErrInvalidChannels = errors.New("encoder: invalid channels (must be 1 or 2)")

	// ErrInvalidFrameSize indicates an invalid frame size.
	ErrInvalidFrameSize = errors.New("encoder: invalid frame size")

	// ErrBufferTooSmall indicates the output budget cannot represent the frame.
	ErrBufferTooSmall = errors.New("encoder: output buffer too small")

	// ErrEncodingFailed indicates a general encoding failure.
	ErrEncodingFailed = errors.New("encoder: encoding failed")

	// ErrInvalidDREDDuration indicates DRED duration is outside libopus bounds.
	ErrInvalidDREDDuration = errors.New("encoder: invalid DRED duration")

	// ErrInvalidFECConfig indicates an invalid in-band FEC configuration.
	ErrInvalidFECConfig = errors.New("encoder: invalid in-band FEC config")

	// ErrInvalidVoiceRatio indicates a voice ratio outside libopus bounds.
	ErrInvalidVoiceRatio = errors.New("encoder: invalid voice ratio")
)

const (
	// defaultScratchPacketBytes holds a single-frame packet: the TOC byte plus
	// a 1275-byte frame (opus_encode_frame_native max_data_bytes cap).
	defaultScratchPacketBytes   = libopusMaxDataBytesCap
	extensionScratchPacketBytes = 3826
)

// In-band FEC configuration values accepted by Encoder.SetInBandFEC, matching
// the libopus OPUS_SET_INBAND_FEC argument (src/opus_encoder.c).
const (
	// InBandFECDisabled turns in-band forward error correction off.
	InBandFECDisabled = 0
	// InBandFECEnabled enables LBRR-based FEC for all SILK/Hybrid frames that
	// carry speech (libopus value 1).
	InBandFECEnabled = 1
	// InBandFECMusicSafe enables FEC but lets the encoder suppress it on frames
	// classified as music, trading resilience for quality (libopus value 2).
	InBandFECMusicSafe = 2
)

// Encoder is the unified Opus encoder that orchestrates SILK and CELT sub-encoders.
type Encoder struct {
	// Sub-encoders (created lazily)
	silk        *silk.PacketEncoder
	celtEncoder *celt.Encoder

	// silkMode mirrors libopus st->silk_mode: the controls handed to silk_Encode
	// for the current frame and the status it reports back.
	silkMode silk.EncControl
	// silkPrefillPending marks that scratchSilkPrefill holds the 10 ms of delay
	// history for the silk_Encode prefill of this frame.
	silkPrefillPending bool
	// silkBWSwitch mirrors libopus st->silk_bw_switch: the previous frame
	// signalled an internal SILK bandwidth switch, so this frame carries the
	// CELT->SILK redundant frame and a SILK prefill that keeps the variable LP
	// filter state (prefill 2).
	silkBWSwitch bool
	// nonfinalFrame mirrors libopus st->nonfinal_frame: the frame being coded
	// is a sub-frame before the last one of a multi-frame packet, where SILK
	// cannot switch its bandwidth.
	nonfinalFrame bool
	// frameRangeEncoder and framePayload are the range coder of a frame and
	// its buffer (opus_encode_frame_native's ec_enc over data+1), which also
	// takes the redundant CELT frame after the payload.
	frameRangeEncoder rangecoding.Encoder
	framePayload      []byte
	// redundancyScratch holds a redundant CELT frame of a mode switch.
	redundancyScratch [257]byte
	// frameFinalRange is st->rangeFinal of the last coded frame.
	frameFinalRange uint32
	// prevHBGain is st->prev_HB_gain, the high-band gain of the previous
	// frame that gain_fade() starts from.
	prevHBGain opusVal16
	// hybridStereoWidthQ14 is st->hybrid_stereo_width_Q14, the stereo width
	// the previous frame applied.
	hybridStereoWidthQ14 int16

	// Configuration
	mode              Mode
	bandwidth         types.Bandwidth
	sampleRate        int32
	channels          int32
	frameSize         int32 // Per-channel samples at the configured sample rate.
	lowDelay          bool
	voipApp           bool
	restrictedSilkApp bool

	// Bitrate controls
	bitrateMode   BitrateMode
	useVBR        bool
	vbrConstraint bool
	bitrate       int32 // Target bits per second

	// FEC controls
	fecEnabled        bool
	packetLoss        int32 // Expected packet loss percentage (0-100)
	lastOpusVADActive bool
	lastOpusVADValid  bool
	lastOpusVADProb   float32
	// Getter-only availability: the Opus decision fields also participate in
	// coding, so this bit distinguishes a fresh result from retained state after
	// Reset without changing the encoder's decision state.
	lastOpusVADActivityObserved bool
	// multiFrameDTXCount is the number of internal sub-frames the most recent
	// encode*MultiFramePacket call suppressed via the per-sub-frame DTX decision
	// (libopus opus_encoder.c dtx_count). It is transient per Encode call.
	multiFrameDTXCount int
	// multiFrameLastSubframeDTX records whether the final internal sub-frame of
	// the most recent encode*MultiFramePacket call was DTX-suppressed. libopus
	// reports st->rangeFinal from the last opus_encode_frame_native call in the
	// repacketizer loop, and that call zeroes rangeFinal when it DTXes
	// (opus_encoder.c:2569), so a packet whose last sub-frame is suppressed has a
	// final range of 0. It is transient per Encode call.
	multiFrameLastSubframeDTX bool
	// multiFrameCommitted and multiFrameLastCommitted record whether any
	// SILK/Hybrid sub-frame of the most recent multi-frame packet, and its last
	// one, reached the end-of-frame bookkeeping of opus_encode_frame_native; a
	// SILK DTX sub-frame returns before it. They are transient per Encode call.
	multiFrameCommitted     bool
	multiFrameLastCommitted bool
	fec                     *fecState

	// DTX (Discontinuous Transmission) controls
	dtxEnabled bool
	dtx        *dtxState
	rng        uint32 // RNG for comfort noise
	finalRange uint32

	// Complexity control (0-10, higher = better quality but slower)
	complexity int32

	// Signal type hint for mode selection
	signalType types.Signal

	// Maximum bandwidth limit (actual bandwidth is clamped to this)
	maxBandwidth types.Bandwidth

	// Force channels (-1=auto, 1=mono, 2=stereo)
	forceChannels int32

	// LFE mode flag.
	// When true, force CELT-only narrowband behavior for this stream.
	lfe bool

	// LSB depth of input signal (8-24 bits, affects DTX sensitivity)
	lsbDepth int32

	// Prediction disabled (reduces inter-frame dependency for error resilience)
	predictionDisabled bool

	// Phase inversion disabled (for stereo decorrelation)
	phaseInversionDisabled bool

	// celtEnergyMask carries per-band surround masking into CELT dynalloc control.
	celtEnergyMask []float32

	encoderQEXTFields
	encoderFixedCELTFields
	encoderFixedOuterQ8Fields

	// dnnBlob retains the validated model blob for encoder neural features,
	// matching the lifetime of libopus OPUS_SET_DNN_BLOB.
	dnnBlob *dnnblob.Blob
	encoderDREDFields

	// DC rejection / variable-cutoff HP filter state.
	hpMem [4]float32
	// variableHPSmth2Q15 is the Opus-level smoothed HP cutoff (log2 domain, Q15)
	// driving hp_cutoff() for VoIP input (src/opus_encoder.c). -1 means
	// "not yet initialized" so it is seeded on first use.
	variableHPSmth2Q15    int32
	variableHPSmth2Inited bool

	// Tonality and speech/music analysis.
	analyzer *TonalityAnalysisState
	// Last frame analysis info from RunAnalysis(), used by mode heuristics.
	lastAnalysisInfo    AnalysisInfo
	lastAnalysisValid   bool
	lastAnalysisFresh   bool
	analysisReadPosBak  int32
	analysisSubframeBak int32
	analysisReadBakSet  bool
	prevMode            Mode
	prevPacketMode      Mode
	prevAutoMode        Mode
	// intMode / intBandwidth mirror libopus opus_encoder.c st->mode / st->bandwidth:
	// the internal selected mode/bandwidth state carried between frames. They are
	// initialized to MODE_HYBRID / OPUS_BANDWIDTH_FULLBAND (opus_encoder.c:319-320)
	// and updated to the actual selected values after every full encode. The
	// low-rate "PLC frame" early-exit (opus_encoder.c:1340) reads these STALE values
	// to build its minimal TOC-only packet, before the per-frame mode/bandwidth
	// selection would overwrite them.
	intMode      Mode
	intBandwidth types.Bandwidth
	inputBuffer  []opusRes
	delayBuffer  []opusRes

	// Auto-mode state (matching libopus OpusEncoder fields)
	voiceRatio             int32           // Persistent voice ratio (-1 = unset, 0-100)
	detectedBandwidth      types.Bandwidth // Analysis-detected bandwidth; narrowband is enum value 0.
	detectedBandwidthValid bool            // Distinguishes a valid narrowband result from no analysis result.
	streamChannels         int32           // Actual encoding channels (1 or 2)
	prevChannels           int32           // Previous frame's streamChannels
	autoBandwidth          types.Bandwidth // Last auto-selected bandwidth (for hysteresis)
	first                  bool            // First frame flag
	lbrrCoded              bool            // Previous frame FEC coding decision
	userBandwidth          types.Bandwidth // User-set bandwidth value
	userBandwidthSet       bool            // Whether userBandwidth is explicitly set
	widthMem               StereoWidthMem  // Stateful stereo width computation memory
	toMono                 int32           // Stereo->mono transition countdown (0=inactive)
	fecConfig              int32           // FEC config: 0=disabled, 1=enabled, 2=music-safe

	// pcmBump backs the two frameSize-sized input-domain PCM scratch buffers
	// (scratchInputPCM/scratchDCPCM) with one contiguous
	// allocation, carved per-frame at the encode entry and re-carved only when a
	// larger frame is seen (so it sizes to the current frame, not the max).
	pcmBump arena.Bump[opusRes]

	// Scratch buffers for zero-allocation encoding
	scratchDCPCM    []opusRes // DC rejected PCM buffer
	scratchInputPCM []opusRes // Public PCM rounded into the libopus opus_res domain
	scratchPacket   []byte    // Output packet buffer
	// Reusable long-packet assembly scratch (40/60/80/100/120 ms paths).
	scratchFrameSlots       [6][]byte // Per-subframe slice headers for long packets
	scratchFrameBytes       []byte    // Backing storage for kept subframe payloads
	scratchQEXTPayloadBytes []byte    // Backing storage for kept QEXT payloads
	scratchDelayedPCM       []opusRes // Delay-compensated CELT input
	// Snapshot of libopus delay-history CELT transition prefill window (Fs/400).
	scratchTransitionPrefill []opusRes
	scratchSilkPrefill       []opusRes
	scratchCELTPrefill       []opusRes // CELT transition prefill source (Fs/400 * channels)
	hasCELTPrefill           bool
	floatInputFrame          []float32 // Current public float32 frame view, if available
	floatInputExact          bool      // True when pcm originated from float32 samples
}

// NewEncoder creates an Opus encoder for sampleRate and channels. Unsupported
// sample rates fall back to 48 kHz; 96 kHz is retained when QEXT is enabled.
// Channel counts below one select mono, and counts above two select stereo.
func NewEncoder(sampleRate, channels int) *Encoder {
	switch sampleRate {
	case 8000, 12000, 16000, 24000, 48000:
	case 96000:
		if !extsupport.QEXT {
			sampleRate = 48000
		}
	default:
		sampleRate = 48000
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	e := &Encoder{
		mode:                   ModeAuto,
		bandwidth:              types.BandwidthFullband,
		sampleRate:             int32(sampleRate),
		channels:               int32(channels),
		frameSize:              int32(sampleRate / 50),
		lowDelay:               false,
		bitrateMode:            ModeCVBR,
		useVBR:                 true,
		vbrConstraint:          true,
		bitrate:                64000,
		fecEnabled:             false,
		packetLoss:             0,
		fec:                    newFECState(),
		dtxEnabled:             false,
		dtx:                    newDTXState(),
		rng:                    22222,
		complexity:             9,
		signalType:             types.SignalAuto,
		maxBandwidth:           types.BandwidthFullband,
		forceChannels:          -1,
		lsbDepth:               24,
		predictionDisabled:     false,
		phaseInversionDisabled: false,
		analyzer:               NewTonalityAnalysisState(sampleRate),
		scratchPacket:          make([]byte, defaultScratchPacketBytes),
		prevMode:               ModeAuto,
		prevPacketMode:         ModeAuto,
		prevAutoMode:           ModeAuto,
		intMode:                ModeHybrid,
		intBandwidth:           types.BandwidthFullband,
		voiceRatio:             -1,
		streamChannels:         int32(channels),
		// opus_encoder_init zeroes the state before setting stream_channels;
		// prev_channels stays zero until the first encoded frame.
		prevChannels:              0,
		autoBandwidth:             types.BandwidthFullband,
		first:                     true,
		prevHBGain:                1,
		hybridStereoWidthQ14:      1 << 14,
		encoderFixedOuterQ8Fields: newFixedOuterQ8Fields(),
	}
	return e
}

// SetMode sets the encoding mode.
func (e *Encoder) SetMode(mode Mode) {
	e.mode = mode
}

// Mode returns the configured encoding mode. ModeAuto lets the encoder select
// a coding mode for each frame.
func (e *Encoder) Mode() Mode {
	return e.mode
}

// FirstFrameCoded reports whether a frame has been committed since the encoder
// was created or reset, mirroring libopus !st->first.
//
// C ref: opus_encoder.c clears st->first = 0 only after a frame reaches the end
// of opus_encode_native (line 2562), AFTER the SILK nBytes==0 early return
// (line 2242) which leaves st->first = 1. OPUS_SET_APPLICATION uses
// !st->first to reject an application change once a frame has been coded.
func (e *Encoder) FirstFrameCoded() bool {
	return !e.first
}

// SetLowDelay toggles low-delay application behavior.
//
// When enabled, CELT delay compensation is disabled to match restricted
// low-delay semantics.
func (e *Encoder) SetLowDelay(enabled bool) {
	e.lowDelay = enabled
}

// LowDelay reports whether low-delay application behavior is enabled.
func (e *Encoder) LowDelay() bool {
	return e.lowDelay
}

// SetVoIPApplication toggles VoIP application bias for mode decisions.
func (e *Encoder) SetVoIPApplication(enabled bool) {
	e.voipApp = enabled
}

// VoIPApplication reports whether VoIP application bias is enabled.
func (e *Encoder) VoIPApplication() bool {
	return e.voipApp
}

// SetRestrictedSilkApplication toggles restricted-SILK application behavior.
func (e *Encoder) SetRestrictedSilkApplication(enabled bool) {
	e.restrictedSilkApp = enabled
}

// SetVoiceRatio sets the private libopus voice-ratio control.
func (e *Encoder) SetVoiceRatio(ratio int) error {
	if ratio < -1 || ratio > 100 {
		return ErrInvalidVoiceRatio
	}
	e.voiceRatio = int32(ratio)
	return nil
}

// VoiceRatio returns the current private libopus voice-ratio control value.
func (e *Encoder) VoiceRatio() int {
	return int(e.voiceRatio)
}

// SetBandwidth sets the target audio bandwidth.
func (e *Encoder) SetBandwidth(bandwidth types.Bandwidth) {
	// C ref: opus_encoder.c OPUS_SET_BANDWIDTH writes only st->user_bandwidth;
	// st->bandwidth (the decided value reported by OPUS_GET_BANDWIDTH) is left
	// untouched and recomputed during encode. Keep e.bandwidth as the decided
	// value so the getter mirrors libopus get-after-set.
	e.userBandwidth = bandwidth
	e.userBandwidthSet = true
	if e.celtEncoder != nil {
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	}
}

// SetBandwidthAuto clears an explicit bandwidth request and restores automatic selection.
func (e *Encoder) SetBandwidthAuto() {
	e.userBandwidth = 0
	e.userBandwidthSet = false
	if e.celtEncoder != nil {
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	}
}

// Bandwidth returns the current bandwidth setting.
func (e *Encoder) Bandwidth() types.Bandwidth {
	return e.bandwidth
}

// DNNBlobLoaded reports whether a validated model blob is retained.
func (e *Encoder) DNNBlobLoaded() bool {
	return e.dnnBlob != nil
}

// frame20ms returns the number of native-Fs samples in a 20 ms frame (Fs/50).
// This is the libopus opus_encode_native multi-frame split unit and the upper
// bound for a single CELT/Hybrid encode (longer frames are split). At 48 kHz it
// is 960 samples, the standard 20 ms frame size.
func (e *Encoder) frame20ms() int {
	return int(e.sampleRate) / 50
}

// isMultiFramePacket reports whether the given mode/frameSize is encoded as an
// Opus multi-frame packet (N internal 20ms — or for SILK 20/40/60ms — sub-frames
// repacketized together). This mirrors libopus opus_encode_native's condition at
// opus_encoder.c:1698: any >20ms CELT/Hybrid packet, or any SILK packet >60ms.
// For these packets the per-sub-frame DTX decision and Opus-level activity are
// handled inside the encode*MultiFramePacket loop, not at the whole-frame level.
func (e *Encoder) isMultiFramePacket(mode Mode, frameSize int) bool {
	f20 := e.frame20ms()
	if frameSize <= 0 || f20 <= 0 || frameSize%f20 != 0 {
		return false
	}
	switch mode {
	case ModeCELT, ModeHybrid:
		return frameSize > f20
	case ModeSILK:
		return frameSize > 3*f20
	default:
		return false
	}
}

// multiFrameSubframeCount returns how many internal sub-frames a multi-frame
// packet of this mode/frameSize is split into, matching the encode loop counts:
// CELT/Hybrid split into frameSize/20ms sub-frames; SILK splits 80ms->2x40ms,
// 100ms->5x20ms, 120ms->2x60ms (libopus opus_encoder.c:1713-1725). Returns 0 if
// the packet is not a multi-frame packet.
func (e *Encoder) multiFrameSubframeCount(mode Mode, frameSize int) int {
	if !e.isMultiFramePacket(mode, frameSize) {
		return 0
	}
	f20 := e.frame20ms()
	switch mode {
	case ModeCELT, ModeHybrid:
		return frameSize / f20
	case ModeSILK:
		switch frameSize {
		case 4 * f20: // 80 ms -> 2x40 ms
			return 2
		case 5 * f20: // 100 ms -> 5x20 ms
			return 5
		case 6 * f20: // 120 ms -> 2x60 ms
			return 2
		}
	}
	return 0
}

// SetFrameSize stores the per-channel frame size in samples at the configured
// input sample rate.
func (e *Encoder) SetFrameSize(frameSize int) {
	e.frameSize = int32(frameSize)
}

// FrameSize returns the configured per-channel frame size in samples at the
// input sample rate.
func (e *Encoder) FrameSize() int {
	return int(e.frameSize)
}

// Channels returns the number of audio channels (1 or 2).
func (e *Encoder) Channels() int {
	return int(e.channels)
}

// SampleRate returns the input sample rate.
func (e *Encoder) SampleRate() int {
	return int(e.sampleRate)
}

// Reset clears the encoder state for a new stream.
func (e *Encoder) Reset() {
	// hp_mem is inside OPUS_ENCODER_RESET_START and is cleared by
	// OPUS_RESET_STATE (opus_encoder.c:112-121, 3254).
	e.hpMem = [4]float32{}
	e.variableHPSmth2Q15 = 0
	e.variableHPSmth2Inited = false
	if len(e.delayBuffer) > 0 {
		clear(e.delayBuffer)
	}
	if len(e.inputBuffer) > 0 {
		e.inputBuffer = e.inputBuffer[:0]
	}
	if e.silk != nil {
		e.silk.Init()
	}
	e.silkPrefillPending = false
	e.silkBWSwitch = false
	e.nonfinalFrame = false
	e.frameFinalRange = 0
	e.prevHBGain = 1
	e.hybridStereoWidthQ14 = 1 << 14
	e.resetFixedOuterQ8()
	if e.celtEncoder != nil {
		e.celtEncoder.Reset()
		e.syncQEXTToCELT()
	}
	e.resetFixedCELT()
	if len(e.celtEnergyMask) > 0 {
		clear(e.celtEnergyMask)
		e.celtEnergyMask = e.celtEnergyMask[:0]
	}
	e.resetFECState()
	if e.dtx != nil {
		e.dtx.reset()
	}
	e.finalRange = 0
	if e.analyzer != nil {
		e.analyzer.Reset()
	}
	e.lastAnalysisValid = false
	e.lastAnalysisFresh = false
	e.analysisReadBakSet = false
	e.lastOpusVADActivityObserved = false
	e.prevMode = ModeAuto
	e.prevPacketMode = ModeAuto
	e.prevAutoMode = ModeAuto
	e.intMode = ModeHybrid
	e.intBandwidth = types.BandwidthFullband
	e.detectedBandwidth = 0
	e.detectedBandwidthValid = false
	// C ref: opus_encoder.c OPUS_RESET_STATE sets st->bandwidth =
	// OPUS_BANDWIDTH_FULLBAND. st->bandwidth (the decided bandwidth reported by
	// OPUS_GET_BANDWIDTH) sits after OPUS_ENCODER_RESET_START, so the reset
	// region clears it and the handler re-seeds it to FULLBAND. The user
	// bandwidth request (userBandwidth/userBandwidthSet) is before the reset
	// start and is preserved.
	e.bandwidth = types.BandwidthFullband
	e.streamChannels = int32(e.channels)
	// OPUS_RESET_STATE clears prev_channels along with the other frame state;
	// only stream_channels is then re-seeded (opus_encoder.c:3249-3265).
	e.prevChannels = 0
	e.autoBandwidth = types.BandwidthFullband
	e.first = true
	e.widthMem = StereoWidthMem{}
	e.toMono = 0
	if extsupport.DREDRuntime {
		e.resetDREDControls()
	}
}

// SetFEC enables or disables in-band Forward Error Correction.
func (e *Encoder) SetFEC(enabled bool) {
	config := InBandFECDisabled
	if enabled {
		config = InBandFECEnabled
	}
	_ = e.SetInBandFEC(config)
}

// SetInBandFEC sets the libopus-compatible in-band FEC configuration.
func (e *Encoder) SetInBandFEC(config int) error {
	if config < InBandFECDisabled || config > InBandFECMusicSafe {
		return ErrInvalidFECConfig
	}
	e.fecConfig = int32(config)
	e.fecEnabled = config != InBandFECDisabled
	if e.fecEnabled && e.fec == nil {
		e.fec = newFECState()
	}
	return nil
}

// FECEnabled returns whether FEC is enabled.
func (e *Encoder) FECEnabled() bool {
	return e.fecEnabled
}

// InBandFEC returns the in-band FEC configuration.
func (e *Encoder) InBandFEC() int {
	return int(e.fecConfig)
}

// SetPacketLoss sets the expected packet loss percentage (0-100).
func (e *Encoder) SetPacketLoss(lossPercent int) {
	if lossPercent < 0 {
		lossPercent = 0
	}
	if lossPercent > 100 {
		lossPercent = 100
	}
	e.packetLoss = int32(lossPercent)
	if e.celtEncoder != nil {
		e.celtEncoder.SetPacketLoss(int(e.packetLoss))
	}
}

// PacketLoss returns the expected packet loss percentage.
func (e *Encoder) PacketLoss() int {
	return int(e.packetLoss)
}

// SetDTX enables or disables Discontinuous Transmission.
func (e *Encoder) SetDTX(enabled bool) {
	e.dtxEnabled = enabled
	if enabled && e.dtx == nil {
		e.dtx = newDTXState()
	}
}

// DTXEnabled returns whether DTX is enabled.
func (e *Encoder) DTXEnabled() bool {
	return e.dtxEnabled
}

// SetComplexity sets encoder complexity (0-10).
func (e *Encoder) SetComplexity(complexity int) {
	if complexity < 0 {
		complexity = 0
	}
	if complexity > 10 {
		complexity = 10
	}
	e.complexity = int32(complexity)
	if e.celtEncoder != nil {
		e.celtEncoder.SetComplexity(complexity)
	}
}

// Complexity returns the current complexity setting.
func (e *Encoder) Complexity() int {
	return int(e.complexity)
}

// FinalRange returns the final range coder state after encoding.
func (e *Encoder) FinalRange() uint32 {
	return e.finalRange
}

// SetBitrateMode sets the bitrate mode (VBR, CVBR, or CBR).
func (e *Encoder) SetBitrateMode(mode BitrateMode) {
	switch mode {
	case ModeCBR:
		e.useVBR = false
	case ModeCVBR:
		e.useVBR = true
		e.vbrConstraint = true
	case ModeVBR:
		e.useVBR = true
		e.vbrConstraint = false
	default:
		e.useVBR = true
		e.vbrConstraint = false
	}
	e.bitrateMode = modeFromVBRFlags(e.useVBR, e.vbrConstraint)
}

// BitrateMode returns the current bitrate mode.
func (e *Encoder) GetBitrateMode() BitrateMode {
	return modeFromVBRFlags(e.useVBR, e.vbrConstraint)
}

// SetVBR enables/disables VBR while preserving the existing constraint setting.
func (e *Encoder) SetVBR(enabled bool) {
	e.useVBR = enabled
	e.bitrateMode = modeFromVBRFlags(e.useVBR, e.vbrConstraint)
}

// VBR reports whether VBR is enabled.
func (e *Encoder) VBR() bool {
	return e.useVBR
}

// SetVBRConstraint toggles VBR constraint without forcing VBR on/off.
func (e *Encoder) SetVBRConstraint(constrained bool) {
	e.vbrConstraint = constrained
	e.bitrateMode = modeFromVBRFlags(e.useVBR, e.vbrConstraint)
}

// VBRConstraint reports whether constrained VBR is enabled.
func (e *Encoder) VBRConstraint() bool {
	return e.vbrConstraint
}

func modeFromVBRFlags(useVBR, vbrConstraint bool) BitrateMode {
	if !useVBR {
		return ModeCBR
	}
	if vbrConstraint {
		return ModeCVBR
	}
	return ModeVBR
}

// SetBitrate sets the target bitrate in bits per second.
func (e *Encoder) SetBitrate(bitrate int) {
	e.bitrate = int32(clampBitrateForChannels(bitrate, int(e.channels)))
}

// SetAllocatedBitrate sets a bitrate allocated by the multistream encoder.
func (e *Encoder) SetAllocatedBitrate(bitrate int) {
	e.bitrate = int32(clampAllocatedBitrate(bitrate, int(e.channels)))
}

// Bitrate returns the current target bitrate.
func (e *Encoder) Bitrate() int {
	return int(e.bitrate)
}

func (e *Encoder) resolvedBitrateForFrame(frameSize, maxDataBytes int) int {
	return resolveUserBitrate(int(e.bitrate), int(e.sampleRate), int(e.channels), frameSize, maxDataBytes)
}

func (e *Encoder) maxRateForFrame(frameSize, maxDataBytes int) int {
	if frameSize <= 0 || maxDataBytes <= 0 {
		return 0
	}
	return maxDataBytes * 8 * int(e.sampleRate) / frameSize
}

// bitrateToBitsFs mirrors libopus celt.h bitrate_to_bits():
// bitrate*6/(6*Fs/frame_size).
func bitrateToBitsFs(bitrate, fs, frameSize int) int {
	d := 6 * fs / frameSize
	if d == 0 {
		return 0
	}
	return bitrate * 6 / d
}

// bitsToBitrateFs mirrors libopus celt.h bits_to_bitrate(): bits*(6*Fs/frame_size)/6.
func bitsToBitrateFs(bits, fs, frameSize int) int {
	return bits * (6 * fs / frameSize) / 6
}

// silkTotalBitrate returns the total_bitRate opus_encode_frame_native splits
// between SILK and CELT: the frame budget bits_target =
// IMIN(8*(max_data_bytes-redundancy_bytes), bitrate_to_bits(bitrate_bps)) - 8
// (src/opus_encoder.c:1960), which reserves the TOC byte, converted back to a
// rate by bits_to_bitrate (src/opus_encoder.c:2051). maxDataBytes is the
// frame's orig_max_data_bytes; the 1276-byte cap is applied here.
func (e *Encoder) silkTotalBitrate(frameSize, maxDataBytes, redundancyBytes int) int {
	sampleRate := int(e.sampleRate)
	maxDataBytes = min(maxDataBytes, libopusMaxDataBytesCap)
	bitsTarget := min(8*(maxDataBytes-redundancyBytes), bitrateToBitsFs(int(e.bitrate), sampleRate, frameSize)) - 8
	return bitsToBitrateFs(bitsTarget, sampleRate, frameSize)
}

// computeEquivRate calculates the equivalent bitrate based on frame rate, VBR mode,
// complexity, and packet loss. Matches libopus compute_equiv_rate().
func (e *Encoder) computeEquivRate(bitrate, channels, frameRate int32, vbr bool, actualMode Mode, complexity, loss int32) int32 {
	equiv := bitrate
	if frameRate > 50 {
		equiv -= (40*channels + 20) * (frameRate - 50)
	}
	if !vbr {
		equiv -= equiv / 12
	}
	equiv = (equiv * (90 + complexity)) / 100
	switch actualMode {
	case ModeSILK, ModeHybrid:
		if complexity < 2 {
			equiv = (equiv * 4) / 5
		}
		if loss > 0 {
			equiv -= (equiv * loss) / (6*loss + 10)
		}
	case ModeCELT:
		if complexity < 5 {
			equiv = (equiv * 9) / 10
		}
	default:
		// Mode not known yet: libopus applies half the SILK packet-loss penalty.
		if loss > 0 {
			equiv -= (equiv * loss) / (12*loss + 20)
		}
	}
	return equiv
}

// Encode encodes a frame of libopus float-build PCM audio to an Opus packet.
func (e *Encoder) Encode(pcm []float32, frameSize int) ([]byte, error) {
	return e.EncodeWithAnalysis(pcm, frameSize, pcm)
}

// EncodeFloat32 encodes a frame of libopus float PCM audio to an Opus packet.
func (e *Encoder) EncodeFloat32(pcm []float32, frameSize int) ([]byte, error) {
	return e.Encode(pcm, frameSize)
}

// EncodeFloat32WithAnalysisMaxBytes is the float32 PCM entrypoint matching
// libopus opus_encode_float().
func (e *Encoder) EncodeFloat32WithAnalysisMaxBytes(pcm []float32, frameSize int, analysisPCM []float32, maxDataBytes int) ([]byte, error) {
	return e.EncodeWithAnalysisMaxBytes(pcm, frameSize, analysisPCM, maxDataBytes)
}

// EncodeWithAnalysis encodes the selected frame while allowing analysis to see
// a larger caller frame, matching libopus expert-frame-duration handling.
func (e *Encoder) EncodeWithAnalysis(pcm []float32, frameSize int, analysisPCM []float32) ([]byte, error) {
	return e.EncodeWithAnalysisMaxBytes(pcm, frameSize, analysisPCM, maxSilkPacketBytes)
}

// EncodeWithAnalysisMaxBytes encodes with a caller output budget. maxDataBytes
// mirrors libopus max_data_bytes after packet-size-cap clamping.
func (e *Encoder) EncodeWithAnalysisMaxBytes(pcm []float32, frameSize int, analysisPCM []float32, maxDataBytes int) ([]byte, error) {
	channels := int(e.channels)
	expectedLen := frameSize * channels
	if len(pcm) != expectedLen {
		return nil, ErrInvalidFrameSize
	}
	if analysisPCM == nil {
		analysisPCM = pcm
	}
	if len(analysisPCM) < expectedLen || len(analysisPCM)%channels != 0 {
		return nil, ErrInvalidFrameSize
	}
	inputPCM := e.prepareOpusResInput(pcm)
	e.prepareFixedInputRes(pcm)
	defer e.clearFixedInputRes()
	e.SetFloatInputFrame(pcm)
	defer e.ClearFloatInputFrame()
	return e.encodeOpusResWithAnalysisMaxBytes(inputPCM, frameSize, maxDataBytes, func() {
		e.refreshFrameAnalysisF32(analysisPCM, frameSize)
	})
}

// EncodeShortMixedWithAnalysisMaxBytes encodes the opus_res samples produced by
// libopus's short input callback. analysisPCM contains the original short input
// channels, converted exactly to float32 by division by 32768 before routing;
// it is separate from any projection-mixed coding samples.
func (e *Encoder) EncodeShortMixedWithAnalysisMaxBytes(pcm []float32, frameSize int, analysisPCM []float32, maxDataBytes int) ([]byte, error) {
	channels := int(e.channels)
	expectedLen := frameSize * channels
	if len(pcm) != expectedLen || frameSize <= 0 {
		return nil, ErrInvalidFrameSize
	}
	if len(analysisPCM) < expectedLen || len(analysisPCM)%channels != 0 {
		return nil, ErrInvalidFrameSize
	}
	inputPCM := e.prepareOpusResInput(pcm)
	e.prepareFixedInputRes(pcm)
	defer e.clearFixedInputRes()
	// opus_encode_native uses min(16, st->lsb_depth) for this call while the
	// configured control survives the call.
	configuredDepth := e.lsbDepth
	if e.lsbDepth > 16 {
		e.lsbDepth = 16
	}
	if e.analyzer != nil {
		e.analyzer.SetLSBDepth(int(e.lsbDepth))
	}
	defer func() {
		e.lsbDepth = configuredDepth
		if e.analyzer != nil {
			e.analyzer.SetLSBDepth(int(configuredDepth))
		}
	}()
	e.ClearFloatInputFrame()
	return e.encodeOpusResWithAnalysisMaxBytes(inputPCM, frameSize, maxDataBytes, func() {
		e.refreshFrameAnalysisF32(analysisPCM, frameSize)
	})
}

func (e *Encoder) prepareOpusResInput(pcm []float32) []opusRes {
	// The two frame-sized input buffers share one reusable arena.
	if len(pcm) > 0 {
		e.pcmBump.Ensure(2 * len(pcm))
		e.scratchInputPCM = e.pcmBump.AllocN(len(pcm))
		e.scratchDCPCM = e.pcmBump.AllocN(len(pcm))
	}
	inputPCM := e.ensureInputPCM(len(pcm))
	copy(inputPCM, pcm)
	return inputPCM
}

// encodeOpusResWithAnalysisMaxBytes is the core single-frame encode pipeline,
// the Go counterpart of libopus opus_encode_native (src/opus_encoder.c). All
// public Encode* entry points funnel here after converting their input to the
// internal opusRes representation.
//
// inputPCM is one frame of interleaved samples (frameSize per channel);
// maxDataBytes is the caller's output budget after packet-size clamping; and
// refreshAnalysis, if non-nil, runs the tonality analyzer on the untouched input
// before any high-pass/DC processing, matching libopus run_analysis ordering.
//
// The function applies the input high-pass/DC filters and SILK cutoff update in
// libopus order, handles the low-space TOC-only path, selects mode and bandwidth,
// performs delay compensation and transition prefill, runs SILK/CELT/Hybrid, and
// assembles the packet. It returns nil if the buffered input does not yet contain
// a complete frame. Invalid frame sizes, insufficient output budgets, and
// encoding failures return ErrInvalidFrameSize, ErrBufferTooSmall, or
// ErrEncodingFailed, respectively.
func (e *Encoder) encodeOpusResWithAnalysisMaxBytes(inputPCM []opusRes, frameSize int, maxDataBytes int, refreshAnalysis func()) ([]byte, error) {
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	// opus_encode_native clears rangeFinal at entry, including calls that emit
	// a TOC-only packet through the low-space return path.
	e.finalRange = 0
	// A non-positive frame size is never a valid Opus duration; reject it before
	// any sampleRate/frameSize division (libopus opus_encode_native returns
	// OPUS_BAD_ARG). Without this guard frameSize==0 passes the length check below
	// (expectedLen==0) and divides by zero in the frame-rate computation.
	if frameSize <= 0 {
		return nil, ErrInvalidFrameSize
	}
	expectedLen := frameSize * channels
	if len(inputPCM) != expectedLen {
		return nil, ErrInvalidFrameSize
	}
	if maxDataBytes <= 0 {
		return nil, ErrEncodingFailed
	}
	// Just avoid insane packet sizes here; the per-frame caps apply later.
	packetCapBytes := e.maxOutputPacketBytes()
	if maxDataBytes > packetCapBytes {
		maxDataBytes = packetCapBytes
	}
	// opus_encode_native rejects a 100 ms frame when the caller provides only a
	// TOC byte. It clears rangeFinal above, then returns before analysis and
	// encoder-state updates (src/opus_encoder.c:1232-1238).
	if maxDataBytes == 1 && sampleRate == frameSize*10 {
		return nil, ErrBufferTooSmall
	}
	// e.bitrate carries st->bitrate_bps for this frame: the user bitrate bounded
	// by the output budget (user_bitrate_to_bitrate) and, in CBR, rounded to the
	// whole-byte frame size below.
	userBitrate := e.bitrate
	defer func() {
		e.bitrate = userBitrate
	}()
	e.bitrate = int32(e.resolvedBitrateForFrame(frameSize, maxDataBytes))
	isSilence := isDigitalSilenceRes(inputPCM, e.lsbDepth)
	e.hasCELTPrefill = false
	e.silkPrefillPending = false
	e.clearFixedCELTUsed()
	defer func() {
		e.analysisReadBakSet = false
	}()
	// Run Opus analysis on the original input frame (before top-level dc_reject)
	// to match libopus run_analysis ordering.
	if refreshAnalysis != nil {
		refreshAnalysis()
	}
	// opus_encode_native refreshes detected_bandwidth for every call before
	// either the automatic or user-forced mode branch consumes it.
	e.updateDetectedBandwidth()
	lookaheadSamples := 0
	vadPCM := inputPCM
	frameEnd := frameSize * channels
	samplesNeeded := frameEnd + lookaheadSamples
	directFrameInput := lookaheadSamples == 0 && len(e.inputBuffer) == 0
	// rawFramePCM is the caller frame before the Opus-level high-pass: libopus
	// reads it for compute_stereo_width() and the mode decisions, and only
	// opus_encode_frame_native() applies dc_reject()/hp_cutoff() into pcm_buf.
	var rawFramePCM []opusRes
	if directFrameInput {
		rawFramePCM = inputPCM[:frameEnd]
	} else {
		e.inputBuffer = append(e.inputBuffer, inputPCM...)
		if len(e.inputBuffer) < samplesNeeded {
			return nil, nil
		}
		rawFramePCM = e.inputBuffer[:frameEnd]
	}

	stereoWidth := e.frameStereoWidth(rawFramePCM, frameSize)

	// libopus "too little space" fast path (opus_encoder.c:1340). The resolved
	// bitrate is already in e.bitrate; derive the CBR-clamped budget and effective
	// bitrate, then emit a minimal TOC-only packet when neither the byte budget
	// nor the bitrate can support a real encode. This mirrors the per-stream
	// curr_max squeeze the multistream encoder applies to high-channel layouts.
	frameRate := sampleRate / frameSize
	if frameRate <= 0 {
		frameRate = 1
	}
	cbrMaxDataBytes := maxDataBytes
	if e.bitrateMode == ModeCBR {
		// src/opus_encoder.c:1327-1334: CBR codes whole bytes, so the frame
		// bitrate is the one the rounded byte count carries.
		cbrBytes := min((bitrateToBitsFs(int(e.bitrate), sampleRate, frameSize)+4)/8, maxDataBytes)
		e.bitrate = int32(bitsToBitrateFs(cbrBytes*8, sampleRate, frameSize))
		cbrMaxDataBytes = max(1, cbrBytes)
	}
	primaryBitrate := e.bitrate
	encodingBitrate := primaryBitrate
	dredBitrate := 0
	if e.dredEncodingActive() {
		if plan, ok := e.computeDREDEmissionPlan(frameSize); ok {
			dredBitrate = int(plan.bitrate)
			encodingBitrate -= plan.bitrate
			if encodingBitrate < 1 {
				encodingBitrate = 1
			}
		}
	}
	// libopus reserves DRED bitrate before its channel and bandwidth decisions
	// (src/opus_encoder.c:1337-1338). Keep the pre-reservation budget separately
	// for packet repacketization, while the selected primary mode uses the reduced
	// per-frame bitrate through primary frame encoding.
	e.bitrate = encodingBitrate
	effBitrate := int(encodingBitrate)
	if cbrMaxDataBytes < 3 || effBitrate < 3*frameRate*8 ||
		(frameRate < 50 && (cbrMaxDataBytes*frameRate < 300 || effBitrate < 2400)) {
		pkt, err := e.emitLowSpacePacket(sampleRate, frameSize, maxDataBytes, cbrMaxDataBytes, effBitrate)
		if err != nil {
			return nil, err
		}
		if !directFrameInput {
			remaining := copy(e.inputBuffer, e.inputBuffer[frameEnd:])
			e.inputBuffer = e.inputBuffer[:remaining]
		}
		return pkt, nil
	}
	// Keep the source frame unfiltered through mode selection. libopus applies
	// hp_cutoff()/dc_reject() inside each opus_encode_frame_native call, after
	// opus_encode_native has split a multi-frame packet.
	// Allow SILK DTX when DTX is on but the generalized DTX cannot be used,
	// e.g. because of the complexity setting or the sample rate
	// (src/opus_encoder.c:1458-1464).
	e.silkMode.UseDTX = e.dtxEnabled && !e.lastAnalysisValid && !isSilence

	var actualMode, prevModeNext Mode
	if e.mode == ModeAuto || e.lowDelay {
		// Full libopus mode and bandwidth decision chain: voice_ratio,
		// stereo_width, stream_channels, auto-bandwidth, bandwidth clamping,
		// decide_fec, and mode fixup. Low-delay applications pin CELT at
		// opus_encoder.c:1470 and still run bandwidth selection.
		actualMode, prevModeNext = e.autoModeAndBandwidthDecision(stereoWidth, frameSize, cbrMaxDataBytes, isSilence)
	} else {
		signalHint := e.signalType
		if signalHint == types.SignalAuto {
			signalHint = e.autoSignalFromPCM(rawFramePCM, frameSize)
		}
		e.updateStreamChannelsForFrame(frameSize)
		requestedMode := e.selectMode(frameSize, signalHint)
		if e.lfe {
			requestedMode = ModeCELT
		}
		if requestedMode != ModeCELT && frameSize < sampleRate/100 {
			requestedMode = ModeCELT
		}
		// The switch into CELT-only precedes the bandwidth clamp and the mode
		// fixup, as in the auto path (src/opus_encoder.c:1533-1557).
		requestedMode, prevModeNext = e.applyCELTTransitionDelay(frameSize, requestedMode)
		// libopus applies the stereo-to-mono delay after choosing the mode and
		// before recomputing the rate used for FEC and bandwidth decisions.
		e.applyStereoToMonoTransition(requestedMode)
		// Run decide_fec for non-auto modes too. In libopus, decide_fec()
		// runs unconditionally at line 1675 (not just in auto mode).
		// This controls whether LBRR is actually coded based on bitrate,
		// bandwidth, packet loss, and hysteresis.
		frameRate := sampleRate / frameSize
		if frameRate <= 0 {
			frameRate = 50
		}
		useVBR := e.bitrateMode != ModeCBR
		equivRate := e.computeEquivRate(e.bitrate, e.streamChannels, int32(frameRate),
			useVBR, requestedMode, e.complexity, e.packetLoss)
		// libopus keeps updating voice_ratio from valid analysis in forced modes
		// because forced CELT still uses it for automatic bandwidth selection.
		e.autoVoiceRatioFromAnalysis()
		e.selectAutoBandwidth(requestedMode, e.autoVoiceEst(), equivRate)
		e.bandwidth = e.autoClampBandwidth(e.bandwidth, requestedMode, equivRate, e.maxRateForFrame(frameSize, cbrMaxDataBytes))
		bw := e.bandwidth
		e.lbrrCoded = decideFEC(e.fecEnabled, e.packetLoss, e.lbrrCoded,
			requestedMode, &bw, equivRate)
		e.bandwidth = bw
		// libopus opus_encoder.c:1688-1695: only the restricted-SILK
		// application pins the bandwidth to WB; a plain forced-SILK request
		// with a wider bandwidth is promoted to Hybrid (and forced Hybrid at
		// <=WB drops to SILK), exactly like the auto path.
		if e.restrictedSilkApp && e.bandwidth > types.BandwidthWideband {
			e.bandwidth = types.BandwidthWideband
		}
		actualMode = autoModeFixup(requestedMode, e.bandwidth)
		if prevModeNext != ModeCELT {
			prevModeNext = actualMode
		}
	}
	transitionToCELT := prevModeNext == ModeCELT && actualMode != ModeCELT
	multiFrame := e.isMultiFramePacket(actualMode, frameSize)
	framePCM := rawFramePCM
	if !multiFrame {
		// A single native frame applies the input filter after mode selection.
		floatOffset := -1
		if directFrameInput {
			floatOffset = 0
		}
		framePCM = e.preprocessInputHPFrame(rawFramePCM, frameSize, actualMode, floatOffset)
		e.preprocessFixedInputRes(frameSize)
	}

	dredExtraDelay := 0
	if !e.lowDelay {
		dredExtraDelay = sampleRate / 250
	}
	f20 := e.frame20ms()
	dredInSubframes := (actualMode == ModeCELT && frameSize > f20 && frameSize%f20 == 0) ||
		(actualMode == ModeHybrid && frameSize > f20 && frameSize%f20 == 0) ||
		(actualMode == ModeSILK && frameSize > 3*f20)
	if e.dredEncodingActive() && !dredInSubframes {
		e.processDREDLatentsForPacket(framePCM, frameSize, dredExtraDelay, actualMode)
	} else if !e.dredEncodingActive() {
		e.clearInactiveDREDHistory()
	}

	// The peak signal energy is tracked once per packet on the unfiltered
	// input; each frame then decides its Opus-level activity
	// (src/opus_encoder.c:1310-1318 and 1911-1930). A multi-frame packet
	// decides it per frame. decide_dtx_mode() runs once the frame is coded, so
	// the encoder state advances before a DTX frame drops the payload
	// (src/opus_encoder.c:2564-2572).
	e.trackPeakSignalEnergy(vadPCM, isSilence)
	if !multiFrame {
		e.updateFrameActivity(vadPCM, isSilence, actualMode)
	}

	var frameData []byte
	var packet []byte
	var err error
	// packetBW is the TOC bandwidth of a single-frame packet: st->bandwidth, or
	// the SILK internal bandwidth for a SILK-only frame (curr_bandwidth).
	packetBW := e.effectiveBandwidth()
	silkDTX := false
	e.multiFrameDTXCount = 0
	e.multiFrameLastSubframeDTX = false
	// A switch between CELT and SILK or Hybrid carries a redundant CELT frame:
	// at the start of the first frame after CELT, or at the end of the last
	// frame before CELT (src/opus_encoder.c:1541-1558).
	celtToSILK := actualMode != ModeCELT && e.prevMode == ModeCELT
	redundancy := celtToSILK || transitionToCELT
	// The packet's equivalent rate after the mode decision
	// (src/opus_encoder.c:1573).
	equivRate := e.computeEquivRate(encodingBitrate, e.streamChannels, int32(sampleRate/frameSize),
		e.bitrateMode != ModeCBR, actualMode, e.complexity, e.packetLoss)
	if actualMode != ModeCELT {
		// The SILK prefill of a switch from CELT, which re-initializes SILK
		// on the first frame of the packet.
		e.maybePrefillSILKOnModeTransition(actualMode, true, true)
	}
	if multiFrame {
		packet, err = e.encodeMultiFramePacket(framePCM, vadPCM, multiFramePacket{
			mode:             actualMode,
			frameSize:        frameSize,
			originalBitrate:  int(primaryBitrate),
			encodingBitrate:  int(encodingBitrate),
			dredBitrate:      dredBitrate,
			dredExtraDelay:   dredExtraDelay,
			outDataBytes:     maxDataBytes,
			equivRate:        equivRate,
			redundancy:       redundancy,
			celtToSILK:       celtToSILK,
			toCELT:           transitionToCELT,
			floatInputDirect: directFrameInput,
		})
	} else {
		dredNoDecision := actualMode != ModeCELT && e.dredEncodingActive() && !e.lastOpusVADValid
		var frame codedFrame
		frame, err = e.encodeFrameNative(framePCM, frameRequest{
			mode:         actualMode,
			frameSize:    frameSize,
			maxDataBytes: cbrMaxDataBytes,
			dredBitrate:  dredBitrate,
			equivRate:    equivRate,
			prevMode:     e.prevMode,
			redundancy:   redundancy,
			celtToSILK:   celtToSILK,
		})
		if err == nil && dredNoDecision {
			e.backfillDREDActivityForFrame(frameSize, e.silkMode.SignalType != 0)
		}
		frameData, packetBW, silkDTX = frame.data, frame.bw, frame.dtx
	}
	// Primary encoding uses the reserved rate; packet assembly, DRED sizing,
	// and CBR/CVBR padding use the original per-frame budget.
	e.bitrate = primaryBitrate
	if err != nil {
		return nil, err
	}
	if !directFrameInput {
		remaining := copy(e.inputBuffer, e.inputBuffer[frameEnd:])
		e.inputBuffer = e.inputBuffer[:remaining]
	}
	// st->mode and st->bandwidth are set before the frame is coded.
	e.intMode = actualMode
	e.intBandwidth = e.bandwidth
	if silkDTX {
		// silk_Encode returned no payload: the packet is the TOC byte alone and
		// the frame ends before the previous mode, the channel history and
		// st->first advance (src/opus_encoder.c:2242-2248).
		e.finalRange = 0
		n, err := BuildPacketInto(e.scratchPacket, nil, modeToTypes(actualMode), packetBW, e.packetTOCFrameSize(frameSize), e.packetStereoForMode(actualMode))
		if err != nil {
			return nil, err
		}
		return e.scratchPacket[:n], nil
	}
	if multiFrame && actualMode != ModeCELT {
		if !e.multiFrameCommitted {
			// Every sub-frame was a SILK DTX frame, so none reached the
			// end-of-frame bookkeeping; the unpadded packet carries only TOCs.
			e.finalRange = 0
			return packet, nil
		}
		if !e.multiFrameLastCommitted {
			// The last sub-frame, which completes a switch to CELT, was a SILK
			// DTX frame: the previous mode comes from an earlier sub-frame.
			prevModeNext = actualMode
		}
	}

	// DTX decision (libopus opus_encoder.c:2564-2572): runs decide_dtx_mode AFTER
	// the frame is fully encoded so the encoder state (SILK NSQ/LPC history, CELT
	// energy memory) is advanced exactly as libopus does. When DTX fires the
	// already-encoded payload is discarded and only the 1-byte TOC is emitted; the
	// decoder runs its own comfort-noise generation when it sees a TOC with no
	// frame data.
	//
	// For multi-frame packets the DTX decision was already made per sub-frame
	// inside the encode*MultiFramePacket loop (mirroring libopus, which runs
	// decide_dtx_mode once per sub-frame). When EVERY sub-frame was suppressed
	// libopus' repacketizer emits an unpadded TOC-only packet — pad is
	// !use_vbr && (dtx_count != nb_frames), so all-DTX => no padding
	// (opus_encoder.c:1831). gopus' multi-frame builder already produced exactly
	// that all-empty packet, so it is returned here before the CBR padding step.
	// A partial DTX (some sub-frames carry payload) keeps its mixed packet and
	// flows through the normal CBR-padding path below. The per-sub-frame path is
	// only taken when DRED is not active; DRED multi-frame packets keep the
	// whole-frame DTX decision (their own packet builder owns the DTX-refresh
	// interaction) and so fall through to the else branch.
	perSubframeDTX := multiFrame && !e.dredEncodingActive()
	if e.dtxEnabled && e.dtx != nil && perSubframeDTX {
		subframeCount := e.multiFrameSubframeCount(actualMode, frameSize)
		if subframeCount > 0 && e.multiFrameDTXCount == subframeCount {
			if isConcreteMode(actualMode) {
				e.prevPacketMode = actualMode
			}
			if isConcreteMode(prevModeNext) {
				e.prevMode = prevModeNext
				if e.mode == ModeAuto {
					e.prevAutoMode = prevModeNext
				}
			}
			e.first = false
			e.prevChannels = e.streamChannels
			e.finalRange = 0
			return packet, nil
		}
	} else if !perSubframeDTX {
		activity := e.resolveDTXActivity()
		if e.decideDTXSuppress(activity, frameSize) {
			if isConcreteMode(actualMode) {
				e.prevPacketMode = actualMode
			}
			if isConcreteMode(prevModeNext) {
				e.prevMode = prevModeNext
				if e.mode == ModeAuto {
					e.prevAutoMode = prevModeNext
				}
			}
			e.first = false
			e.prevChannels = e.streamChannels
			e.finalRange = 0
			return e.buildDTXPacketForMode(frameSize, actualMode, packetBW)
		}
	}

	qextPayload := []byte(nil)
	if extsupport.QEXT && actualMode == ModeCELT && e.celtEncoder != nil {
		qextPayload = e.lastQEXTPayload()
	}
	var qextExtensionBuf [1]packetExtension
	qextExtensions := []packetExtension(nil)
	if len(qextPayload) > 0 {
		qextExtensionBuf[0] = packetExtension{ID: qextExtensionID, Data: qextPayload}
		qextExtensions = qextExtensionBuf[:]
	}
	dredPacketBuilt := false
	if packet == nil {
		stereo := e.packetStereoForMode(actualMode)
		// The TOC config table indexes by the 48 kHz-equivalent frame size
		// (libopus gen_toc derives the period from Fs/frame_size, which is the
		// same duration). For a sub-48 kHz API rate scale the API-rate frameSize
		// up to its 48 kHz core count.
		tocFrameSize := e.packetTOCFrameSize(frameSize)
		if e.dredEncodingActive() {
			if dredPacket, ok, dredErr := e.maybeBuildSingleFrameDREDPacket(frameData, actualMode, packetBW, frameSize, stereo, qextExtensions); dredErr != nil {
				return nil, dredErr
			} else if ok {
				packet = dredPacket
				dredPacketBuilt = true
			}
		}
		var (
			packetLen int
			pktErr    error
		)
		if packet == nil && len(qextPayload) > 0 {
			packetLen, pktErr = buildPacketWithSingleExtensionInto(
				e.scratchPacket,
				frameData,
				modeToTypes(actualMode),
				packetBW,
				tocFrameSize,
				stereo,
				qextExtensionID,
				qextPayload,
				0,
				false,
			)
		} else if packet == nil {
			targetSize := e.targetBytesForBitrate(int(e.bitrate), frameSize)
			if e.bitrateMode == ModeCBR && targetSize >= 2+len(frameData) {
				// A CBR packet is padded to cbr_bytes, which exceeds the
				// single-frame 1276 bytes above 510 kb/s at 20 ms
				// (opus_encode_frame_native pads to max_data_bytes).
				e.ensurePacketScratch(targetSize)
				if targetSize == 2+len(frameData) {
					config := configFromParams(modeToTypes(actualMode), packetBW, tocFrameSize)
					if config < 0 || len(e.scratchPacket) < targetSize {
						pktErr = ErrInvalidConfig
					} else {
						e.scratchPacket[0] = generateTOC(uint8(config), stereo, 3)
						e.scratchPacket[1] = 0x01
						copy(e.scratchPacket[2:], frameData)
						packetLen = targetSize
					}
				} else {
					packetLen, pktErr = buildPacketWithExtensionsInto(
						e.scratchPacket,
						frameData,
						modeToTypes(actualMode),
						packetBW,
						tocFrameSize,
						stereo,
						nil,
						targetSize,
						true,
					)
				}
			} else {
				packetLen, pktErr = BuildPacketInto(e.scratchPacket, frameData, modeToTypes(actualMode), packetBW, tocFrameSize, stereo)
			}
		}
		if packet == nil && pktErr != nil {
			return nil, pktErr
		}
		if packet == nil {
			packet = e.scratchPacket[:packetLen]
		}
	}
	if isConcreteMode(actualMode) {
		e.prevPacketMode = actualMode
	}
	if isConcreteMode(prevModeNext) {
		e.prevMode = prevModeNext
		if e.mode == ModeAuto {
			e.prevAutoMode = prevModeNext
		}
	}
	switch e.bitrateMode {
	case ModeCBR:
		if dredPacketBuilt {
			break
		}
		targetSize := e.targetBytesForBitrate(int(e.bitrate), frameSize)
		if len(qextPayload) > 0 && len(packet) < targetSize {
			stereo := e.packetStereoForMode(actualMode)
			packetLen, pktErr := buildPacketWithSingleExtensionInto(
				e.scratchPacket,
				frameData,
				modeToTypes(actualMode),
				packetBW,
				e.packetTOCFrameSize(frameSize),
				stereo,
				qextExtensionID,
				qextPayload,
				targetSize,
				true,
			)
			if pktErr == nil {
				packet = e.scratchPacket[:packetLen]
			}
		} else {
			packet = padToSizeInto(e.scratchPacket, packet, targetSize)
		}
	case ModeCVBR:
		if !dredPacketBuilt && len(qextPayload) == 0 {
			packet = constrainSize(packet, e.targetBytesForBitrate(int(e.bitrate), frameSize), CVBRTolerance)
		}
	}
	e.prevChannels = e.streamChannels
	// C ref: opus_encode_native sets st->first = 0 here (line 2562), after a
	// frame is committed for every mode (auto, forced SILK/Hybrid/CELT). The
	// low-space and SILK nBytes==0 early returns happen earlier and leave
	// st->first = 1; gopus mirrors that by returning before this point for
	// those cases (emitLowSpacePacket / DTX-suppress set first explicitly).
	e.first = false
	switch {
	case multiFrame && e.multiFrameLastSubframeDTX:
		// libopus reports st->rangeFinal from the last opus_encode_frame_native
		// call in the repacketizer loop. When that final sub-frame DTXes it sets
		// rangeFinal = 0 (opus_encoder.c:2569), so a multi-frame packet whose last
		// internal sub-frame is suppressed has a final range of 0 even though its
		// earlier sub-frames carry payload.
		e.finalRange = 0
	default:
		e.finalRange = e.frameFinalRange
	}
	return packet, nil
}

// emitLowSpacePacket reproduces the libopus opus_encoder.c "too little space to
// do something useful" fast path (lines 1340-1406). When the per-frame byte
// budget or bitrate is too small to run a real encode, libopus emits a minimal
// TOC-only "PLC" packet (1 or 2 bytes), padding it to the CBR budget. The
// multistream encoder squeezes the trailing streams of a high-channel-count
// layout (e.g. third-order ambisonics) down to a 1-2 byte curr_max, which is
// exactly this path; without it gopus errored ("max_data_bytes <= 0") on streams
// that libopus emits as 1-byte minimal packets.
//
// effBitrate is st->bitrate_bps after the CBR cbr_bytes clamp and the DRED
// reservation; cbrMaxDataBytes is the CBR-clamped max_data_bytes (== outDataBytes
// for VBR). outDataBytes is the original caller budget (curr_max).
func (e *Encoder) emitLowSpacePacket(sampleRate, frameSize, outDataBytes, cbrMaxDataBytes, effBitrate int) ([]byte, error) {
	frameRate := sampleRate / frameSize
	if frameRate <= 0 {
		frameRate = 1
	}

	// tocmode = st->mode: the internal selected mode carried between frames, seeded
	// to MODE_HYBRID at init (opus_encoder.c:319). libopus maps an unset st->mode
	// (==0) to MODE_SILK_ONLY (opus_encoder.c:1349); intMode is always concrete here,
	// so that fallback is only defensive.
	tocmode := e.intMode
	if !isConcreteMode(tocmode) {
		tocmode = ModeSILK
	}
	if frameRate > 100 {
		tocmode = ModeCELT
	}

	// bw = st->bandwidth==0 ? NB : st->bandwidth (opus_encoder.c:1345). intBandwidth
	// mirrors st->bandwidth: seeded to FULLBAND at init (opus_encoder.c:320) and
	// updated only after a full encode, so the early-exit reads the same stale value
	// libopus would (OPUS_SET_BANDWIDTH writes user_bandwidth, not st->bandwidth).
	bw := max(e.intBandwidth, types.BandwidthNarrowband)

	packetCode := 0
	numMultiframes := 0

	// 40 ms -> 2 x 20 ms if in CELT_ONLY or HYBRID mode.
	if frameRate == 25 && tocmode != ModeSILK {
		frameRate = 50
		packetCode = 1
	}
	// >= 60 ms frames.
	if frameRate <= 16 {
		if outDataBytes == 1 || (tocmode == ModeSILK && frameRate != 10) {
			tocmode = ModeSILK
			if frameRate <= 12 {
				packetCode = 1
			} else {
				packetCode = 0
			}
			if frameRate == 12 {
				frameRate = 25
			} else {
				frameRate = 16
			}
		} else {
			numMultiframes = 50 / frameRate
			frameRate = 50
			packetCode = 3
		}
	}

	// Per-mode bandwidth clamps (libopus lines 1379-1384).
	switch {
	case tocmode == ModeSILK && bw > types.BandwidthWideband:
		bw = types.BandwidthWideband
	case tocmode == ModeCELT && bw == types.BandwidthMediumband:
		bw = types.BandwidthNarrowband
	case tocmode == ModeHybrid && bw <= types.BandwidthSuperwideband:
		bw = types.BandwidthSuperwideband
	}

	stereo := e.packetStereoForMode(tocmode)
	tocByte := lowSpaceTOC(tocmode, frameRate, bw, stereo)
	tocByte |= byte(packetCode)

	ret := 1
	if packetCode > 1 {
		ret = 2
	}
	// libopus pads to IMAX(cbr_max_data_bytes, ret) for CBR.
	padTarget := max(cbrMaxDataBytes, ret)

	e.ensurePacketScratch(padTarget)
	pkt := e.scratchPacket[:ret]
	pkt[0] = tocByte
	if packetCode == 3 {
		pkt[1] = byte(numMultiframes)
	}

	if e.bitrateMode != ModeCBR {
		return pkt, nil
	}
	// CBR: pad to the budget (opus_packet_pad).
	if padTarget <= ret {
		return pkt, nil
	}
	padded := padToSizeInto(e.scratchPacket, pkt, padTarget)
	return padded, nil
}

// lowSpaceTOC reproduces libopus gen_toc(mode, framerate, bandwidth, channels).
func lowSpaceTOC(mode Mode, framerate int, bw types.Bandwidth, stereo bool) byte {
	period := 0
	for framerate < 400 {
		framerate <<= 1
		period++
	}
	var toc byte
	switch mode {
	case ModeSILK:
		toc = byte((int(bw)-int(types.BandwidthNarrowband))<<5) | byte((period-2)<<3)
	case ModeCELT:
		tmp := max(int(bw)-int(types.BandwidthMediumband), 0)
		toc = 0x80 | byte(tmp<<5) | byte(period<<3)
	default: // Hybrid
		toc = 0x60 | byte((int(bw)-int(types.BandwidthSuperwideband))<<4) | byte((period-2)<<3)
	}
	if stereo {
		toc |= 1 << 2
	}
	return toc
}

// buildDTXPacket builds the TOC-only DTX packet of a frame in the mode
// selectMode picks, at the current bandwidth.
func (e *Encoder) buildDTXPacket(frameSize int) ([]byte, error) {
	actualMode := e.selectMode(frameSize, e.signalType)
	packetBW := e.effectiveBandwidth()
	if actualMode == ModeSILK {
		packetBW = min(packetBW, types.BandwidthWideband)
	}
	return e.buildDTXPacketForMode(frameSize, actualMode, packetBW)
}

// buildDTXPacketForMode assembles the TOC-only packet emitted when DTX fires,
// matching libopus opus_encoder.c, where DTX returns
//
//	data[0] = gen_toc(mode, Fs/frame_size, curr_bandwidth, channels);
//	return 1;
//
// packetBW is the frame's TOC bandwidth (curr_bandwidth). For SILK it is a
// single code-0 frame; for CELT/Hybrid frames longer than 20ms it builds N
// zero-length 20ms sub-frames so the repacketizer collapses them exactly as
// libopus does (code 1 for two sub-frames, code 3 for three). The decoder's
// comfort noise generation runs when it receives a TOC-only packet.
func (e *Encoder) buildDTXPacketForMode(frameSize int, actualMode Mode, packetBW types.Bandwidth) ([]byte, error) {
	stereo := e.packetStereoForMode(actualMode)
	mode := modeToTypes(actualMode)

	// CELT and Hybrid have no single-frame TOC config beyond 20 ms, so a 40/60 ms
	// (>20 ms) packet in those modes is assembled as N=frameSize/960 internal 20 ms
	// frames (encodeCELTMultiFramePacket / encodeHybridMultiFramePacket). When DTX
	// fires for such a frame, libopus' per-sub-frame encode returns tmp_len==1 for
	// every sub-frame and the repacketizer collapses them to a TOC-only packet:
	// code 1 for 2 sub-frames (1 byte) or code 3 with a frame-count byte for 3
	// sub-frames (2 bytes). Mirror that with a multi-frame TOC-only packet built
	// from N zero-length sub-frames at the 20 ms sub-frame config. SILK keeps the
	// single-frame path below because it has native 40/60 ms configs (code 0).
	f20 := e.frame20ms()
	if (mode == types.ModeCELT || mode == types.ModeHybrid) && frameSize > f20 && frameSize%f20 == 0 {
		frameCount := frameSize / f20
		e.resetPacketFrameScratch()
		frames := e.scratchFrameSlots[:0]
		for range frameCount {
			frames = append(frames, e.keepFrame(nil))
		}
		n, err := buildMultiFramePacketInto(e.scratchPacket, frames, mode, packetBW, 960, stereo, false)
		if err != nil {
			return nil, err
		}
		return e.scratchPacket[:n], nil
	}

	// Build TOC-only packet (no frame data) into scratch buffer.
	n, err := BuildPacketInto(e.scratchPacket, nil, mode, packetBW, e.packetTOCFrameSize(frameSize), stereo)
	if err != nil {
		return nil, err
	}
	return e.scratchPacket[:n], nil
}

// modeToTypes converts internal encoder Mode to types.Mode.
func modeToTypes(m Mode) types.Mode {
	switch m {
	case ModeSILK:
		return types.ModeSILK
	case ModeHybrid:
		return types.ModeHybrid
	case ModeCELT:
		return types.ModeCELT
	default:
		return types.ModeCELT
	}
}

func (e *Encoder) silkInternalChannels() int {
	if e.channels != 2 {
		return 1
	}
	streamChannels := e.streamChannels
	if streamChannels <= 0 {
		streamChannels = int32(e.channels)
	}
	if streamChannels <= 1 {
		return 1
	}
	return 2
}

func (e *Encoder) packetStereoForMode(mode Mode) bool {
	if e.channels != 2 {
		return false
	}
	switch mode {
	case ModeSILK:
		return e.silkInternalChannels() == 2
	case ModeHybrid, ModeCELT:
		return e.celtInternalChannelsForMode(mode) == 2
	}
	return true
}

func (e *Encoder) celtInternalChannelsForMode(mode Mode) int {
	if e.channels != 2 {
		return 1
	}
	streamChannels := e.streamChannels
	if streamChannels <= 0 {
		streamChannels = int32(e.channels)
	}
	if (mode == ModeCELT || mode == ModeHybrid) && streamChannels <= 1 {
		return 1
	}
	return 2
}

// preprocessInputHP applies the input high-pass stage that precedes SILK/CELT,
// matching src/opus_encoder.c: VoIP uses the adaptive hp_cutoff() biquad;
// other applications use the fixed 3 Hz dc_reject(), except that an enabled
// QEXT path copies the input directly. The hp_cutoff frequency follows the
// SILK variable-HP-cutoff smoother.
func (e *Encoder) preprocessInputHP(in []opusRes, frameSize int) []opusRes {
	return e.preprocessInputHPFrame(in, frameSize, e.mode, 0)
}

func (e *Encoder) preprocessInputHPFrame(in []opusRes, frameSize int, mode Mode, floatOffset int) []opusRes {
	cutoffHz := e.updateVariableHPCutoff(mode)
	if !e.voipApp {
		if extsupport.QEXT && e.qextActive() {
			return in
		}
		return e.dcRejectFrame(in, frameSize, floatOffset)
	}
	return e.hpCutoffFrameAtCutoff(in, frameSize, floatOffset, cutoffHz)
}

// hpCutoff applies the adaptive second-order high-pass filter used for VoIP
// input, ported from hp_cutoff() + silk_biquad_res() (float path) in
// src/opus_encoder.c. The cutoff frequency adapts from the SILK encoder's
// variable_HP_smth1_Q15 estimate, smoothed at the Opus level into
// variable_HP_smth2_Q15.
func (e *Encoder) hpCutoff(in []opusRes, frameSize int) []opusRes {
	cutoffHz := e.updateVariableHPCutoff(e.mode)
	return e.hpCutoffFrameAtCutoff(in, frameSize, 0, cutoffHz)
}

func (e *Encoder) updateVariableHPCutoff(mode Mode) int32 {
	// opus_encoder.c advances this smoother for every native frame, including
	// non-VoIP frames where the selected filter is dc_reject or QEXT bypass.
	var hpFreqSmth1 int32
	if e.silk != nil && mode != ModeCELT {
		hpFreqSmth1 = e.silk.VariableHPSmth1Q15()
	} else {
		hpFreqSmth1 = silk.MinCutoffLogSmth2Q15()
	}
	if !e.variableHPSmth2Inited {
		e.variableHPSmth2Q15 = silk.InitVariableHPSmth2Q15()
		e.variableHPSmth2Inited = true
	}
	e.variableHPSmth2Q15 = silk.SmoothVariableHPSmth2Q15(e.variableHPSmth2Q15, hpFreqSmth1)
	return silk.VariableHPCutoffHz(e.variableHPSmth2Q15)
}

func (e *Encoder) hpCutoffFrameAtCutoff(in []opusRes, frameSize int, floatOffset int, cutoffHz int32) []opusRes {
	channels := int(e.channels)
	n := frameSize * channels
	out := e.ensureDCPCM(n)
	fs := int(e.sampleRate)
	if fs <= 0 {
		fs = 48000
	}

	bQ28, aQ28 := silk.HPCutoffCoefsQ28(cutoffHz, int32(fs))
	var b [3]float32
	var a [2]float32
	const inv28 = float32(1.0) / float32(int32(1)<<28)
	b[0] = float32(bQ28[0]) * inv28
	b[1] = float32(bQ28[1]) * inv28
	b[2] = float32(bQ28[2]) * inv28
	a[0] = float32(aQ28[0]) * inv28
	a[1] = float32(aQ28[1]) * inv28
	const verySmall = float32(1e-30)

	src32 := e.floatInputFrame
	if !e.floatInputExact || floatOffset < 0 || floatOffset+n > len(src32) {
		src32 = nil
	} else {
		src32 = src32[floatOffset : floatOffset+n]
	}

	// silk_biquad_res follows the selected target's contraction order. AMD64 v3
	// fuses vout and both S[0] terms, then rounds -vout*A[1] before fusing
	// B[2]*inval into S[1]. Keep that chain target-gated; older targets retain
	// their existing recurrence. VERY_SMALL is added to S[1] separately.
	// The src32 branch is
	// hoisted out of the inner loop, and stereo runs both channels' independent
	// recurrences in one interleaved pass so the OoO engine overlaps the two
	// latency-bound filter chains. Per-sample arithmetic is byte-identical to the
	// per-channel form (each channel's state depends only on its own history).
	if channels == 1 {
		s0 := e.hpMem[0]
		s1 := e.hpMem[1]
		if src32 != nil {
			for i := range frameSize {
				inval := src32[i]
				var vout float32
				if outerTargetV3FMA {
					vout = fma32(b[0], inval, s0)
					s0 = fma32(b[1], inval, fma32(-vout, a[0], s1))
					s1 = fma32(b[2], inval, round32(-vout*a[1])) + verySmall
				} else {
					vout = s0 + b[0]*inval
					s0 = s1 - vout*a[0] + b[1]*inval
					s1 = fma32(-vout, a[1], round32(b[2]*inval)) + verySmall
				}
				out[i] = opusRes(vout)
			}
		} else {
			for i := range frameSize {
				inval := float32(in[i])
				var vout float32
				if outerTargetV3FMA {
					vout = fma32(b[0], inval, s0)
					s0 = fma32(b[1], inval, fma32(-vout, a[0], s1))
					s1 = fma32(b[2], inval, round32(-vout*a[1])) + verySmall
				} else {
					vout = s0 + b[0]*inval
					s0 = s1 - vout*a[0] + b[1]*inval
					s1 = fma32(-vout, a[1], round32(b[2]*inval)) + verySmall
				}
				out[i] = opusRes(vout)
			}
		}
		e.hpMem[0] = s0
		e.hpMem[1] = s1
		return out
	}

	s0L := e.hpMem[0]
	s1L := e.hpMem[1]
	s0R := e.hpMem[2]
	s1R := e.hpMem[3]
	if src32 != nil {
		for i := range frameSize {
			l := src32[2*i]
			var voutL float32
			if outerTargetV3FMA {
				voutL = fma32(b[0], l, s0L)
				s0L = fma32(b[1], l, fma32(-voutL, a[0], s1L))
				s1L = fma32(b[2], l, round32(-voutL*a[1])) + verySmall
			} else {
				voutL = s0L + b[0]*l
				s0L = s1L - voutL*a[0] + b[1]*l
				s1L = fma32(-voutL, a[1], round32(b[2]*l)) + verySmall
			}
			out[2*i] = opusRes(voutL)
			r := src32[2*i+1]
			var voutR float32
			if outerTargetV3FMA {
				voutR = fma32(b[0], r, s0R)
				s0R = fma32(b[1], r, fma32(-voutR, a[0], s1R))
				s1R = fma32(b[2], r, round32(-voutR*a[1])) + verySmall
			} else {
				voutR = s0R + b[0]*r
				s0R = s1R - voutR*a[0] + b[1]*r
				s1R = fma32(-voutR, a[1], round32(b[2]*r)) + verySmall
			}
			out[2*i+1] = opusRes(voutR)
		}
	} else {
		for i := range frameSize {
			l := float32(in[2*i])
			var voutL float32
			if outerTargetV3FMA {
				voutL = fma32(b[0], l, s0L)
				s0L = fma32(b[1], l, fma32(-voutL, a[0], s1L))
				s1L = fma32(b[2], l, round32(-voutL*a[1])) + verySmall
			} else {
				voutL = s0L + b[0]*l
				s0L = s1L - voutL*a[0] + b[1]*l
				s1L = fma32(-voutL, a[1], round32(b[2]*l)) + verySmall
			}
			out[2*i] = opusRes(voutL)
			r := float32(in[2*i+1])
			var voutR float32
			if outerTargetV3FMA {
				voutR = fma32(b[0], r, s0R)
				s0R = fma32(b[1], r, fma32(-voutR, a[0], s1R))
				s1R = fma32(b[2], r, round32(-voutR*a[1])) + verySmall
			} else {
				voutR = s0R + b[0]*r
				s0R = s1R - voutR*a[0] + b[1]*r
				s1R = fma32(-voutR, a[1], round32(b[2]*r)) + verySmall
			}
			out[2*i+1] = opusRes(voutR)
		}
	}
	e.hpMem[0] = s0L
	e.hpMem[1] = s1L
	e.hpMem[2] = s0R
	e.hpMem[3] = s1R
	return out
}

// dcRejectFrame applies a first-order DC rejection filter at 3 Hz.
func (e *Encoder) dcRejectFrame(in []opusRes, frameSize int, floatOffset int) []opusRes {
	channels := int(e.channels)
	n := frameSize * channels
	out := e.ensureDCPCM(n)
	fs := int(e.sampleRate)
	if fs <= 0 {
		fs = 48000
	}
	coef := float32(6.3) * float32(3) / float32(fs)
	coef2 := float32(1.0) - coef
	src := in
	if e.floatInputExact && floatOffset >= 0 && floatOffset+n <= len(e.floatInputFrame) {
		src = e.floatInputFrame[floatOffset : floatOffset+n]
	} else {
		src = src[:n]
	}
	out = out[:len(src)]
	verySmall := dcRejectVerySmall[0]
	if channels == 2 {
		m0 := e.hpMem[0]
		m2 := e.hpMem[2]
		for k := 0; k+1 < len(src); k += 2 {
			x0 := src[k]
			x1 := src[k+1]
			out[k] = x0 - m0
			out[k+1] = x1 - m2
			m0 = coef*x0 + verySmall + coef2*m0
			m2 = coef*x1 + verySmall + coef2*m2
		}
		e.hpMem[0] = m0
		e.hpMem[2] = m2
	} else {
		m0 := e.hpMem[0]
		for i, x := range src {
			out[i] = x - m0
			m0 = coef*x + verySmall + coef2*m0
		}
		e.hpMem[0] = m0
	}
	return out
}

// dcRejectVerySmall holds libopus VERY_SMALL as a variable, so the dc_reject
// loops keep it in a register instead of reloading the constant every sample.
var dcRejectVerySmall = [1]float32{1e-30}

func (e *Encoder) ensureInputPCM(size int) []opusRes {
	if cap(e.scratchInputPCM) < size {
		e.scratchInputPCM = make([]opusRes, size)
	}
	return e.scratchInputPCM[:size]
}

func (e *Encoder) ensureDCPCM(size int) []opusRes {
	if cap(e.scratchDCPCM) < size {
		e.scratchDCPCM = make([]opusRes, size)
	}
	return e.scratchDCPCM[:size]
}

func trimSilkTrailingZeros(frameData []byte) []byte {
	for len(frameData) > 2 && frameData[len(frameData)-1] == 0 {
		frameData = frameData[:len(frameData)-1]
	}
	return frameData
}

func (e *Encoder) refreshFrameAnalysisF32(pcm32 []float32, frameSize int) {
	e.lastAnalysisValid = false
	e.lastAnalysisFresh = false
	e.analysisReadBakSet = false
	if e.analyzer == nil || frameSize <= 0 || len(pcm32) == 0 {
		return
	}
	if !e.analysisEnabled() {
		if e.analyzer.Initialized {
			e.analyzer.Reset()
		}
		return
	}
	// Mirror libopus opus_encoder.c: back up analysis read cursor before
	// run_analysis() so long packets can consume per-subframe info later.
	e.analysisReadPosBak = e.analyzer.ReadPos
	e.analysisSubframeBak = e.analyzer.ReadSubframe
	e.analysisReadBakSet = true
	// Keep analysis on float-domain samples to match opus_encode_float / opus_demo -f32.
	info := e.analyzer.RunAnalysis(pcm32, frameSize, int(e.channels))
	if !info.Valid {
		return
	}
	e.lastAnalysisInfo = info
	e.lastAnalysisValid = true
	e.lastAnalysisFresh = true
}

func (e *Encoder) analysisEnabled() bool {
	// Match src/opus_encoder.c opus_encode_native(): fixed-point builds require
	// complexity 10; float builds require complexity 7.
	return !e.restrictedSilkApp && e.complexity >= 7 && (!fixedPointBuild || e.complexity >= 10) && e.sampleRate >= 16000 && e.sampleRate <= 48000
}

// primeSubframeAnalysis advances tonality_get_info() for long packets and keeps
// a reusable per-subframe analysis snapshot for downstream VAD/CELT decisions.
func (e *Encoder) primeSubframeAnalysis(frameSize int) {
	if !e.analysisReadBakSet || e.analyzer == nil {
		return
	}
	info := e.analyzer.tonalityGetInfo(frameSize)
	if info.Valid {
		e.lastAnalysisInfo = info
		e.lastAnalysisValid = true
		e.lastAnalysisFresh = true
		return
	}
	// Keep the last valid snapshot to avoid forcing a fallback RunAnalysis()
	// mid-packet when tonality_get_info has insufficient lookahead.
	if e.lastAnalysisValid {
		e.lastAnalysisFresh = true
	}
}

func (e *Encoder) syncCELTAnalysisToCELT() {
	if e.celtEncoder == nil {
		return
	}
	if !e.lastAnalysisValid {
		e.celtEncoder.SetAnalysisInfoWithTonality(0, [19]uint8{}, 0, 0, 0, 0, false)
		return
	}
	e.celtEncoder.SetAnalysisInfoWithTonality(
		int(e.lastAnalysisInfo.BandwidthIndex),
		e.lastAnalysisInfo.LeakBoost,
		e.lastAnalysisInfo.Activity,
		e.lastAnalysisInfo.Tonality,
		e.lastAnalysisInfo.TonalitySlope,
		e.lastAnalysisInfo.MaxPitchRatio,
		true,
	)
}

func quantizeFloat32ToInt16LibopusInPlace(samples []float32) {
	const invScale = float32(1.0 / 32768.0)
	for i, v := range samples {
		samples[i] = float32(opusmath.Float32ToInt16(v)) * invScale
	}
}

func downmixStereoToSilkMonoLibopus(dst, interleaved []float32, samples int) {
	const invScale = float32(1.0 / 32768.0)
	for i := range samples {
		sum := float32ToInt16Libopus(interleaved[2*i] + interleaved[2*i+1])
		dst[i] = float32(silkRShiftRound1(sum)) * invScale
	}
}

func averageSilkResamplerOutputsLibopus(dst, right []float32, samples int) {
	const invScale = float32(1.0 / 32768.0)
	for i := range samples {
		leftQ0 := float32ToInt16Libopus(dst[i])
		rightQ0 := float32ToInt16Libopus(right[i])
		dst[i] = float32((leftQ0+rightQ0)>>1) * invScale
	}
}

func float32ToInt16Libopus(v float32) int32 {
	return int32(opusmath.Float32ToInt16(v))
}

func silkRShiftRound1(v int32) int32 {
	return (v >> 1) + (v & 1)
}

func (e *Encoder) ensureDelayedPCM(size int) []opusRes {
	if cap(e.scratchDelayedPCM) < size {
		e.scratchDelayedPCM = make([]opusRes, size)
	}
	return e.scratchDelayedPCM[:size]
}

func (e *Encoder) ensureTransitionPrefill(size int) []opusRes {
	if cap(e.scratchTransitionPrefill) < size {
		e.scratchTransitionPrefill = make([]opusRes, size)
	}
	return e.scratchTransitionPrefill[:size]
}

func (e *Encoder) ensureSilkPrefill(size int) []opusRes {
	if cap(e.scratchSilkPrefill) < size {
		e.scratchSilkPrefill = make([]opusRes, size)
	}
	return e.scratchSilkPrefill[:size]
}

func (e *Encoder) ensureCELTPrefill(size int) []opusRes {
	if cap(e.scratchCELTPrefill) < size {
		e.scratchCELTPrefill = make([]opusRes, size)
	}
	return e.scratchCELTPrefill[:size]
}

// delayCompensatedPCM returns pcm_buf of opus_encode_frame_native: the Fs/250
// samples of delay history followed by the start of the frame, the CELT input.
// It leaves the delay buffer as it is and records the CELT transition prefill
// window, libopus tmp_prefill.
func (e *Encoder) delayCompensatedPCM(pcm []opusRes, frameSize int) []opusRes {
	channels := max(int(e.channels), 1)
	frameSamples := min(len(pcm), frameSize*channels)
	sampleRate := int(e.sampleRate)
	delayComp := sampleRate / 250
	if delayComp <= 0 {
		out := e.ensureDelayedPCM(frameSamples)
		copy(out, pcm[:frameSamples])
		return out
	}
	delaySamples := delayComp * channels
	encoderBufferSamples := (sampleRate / 100) * channels
	if delaySamples <= 0 || frameSamples <= 0 {
		out := e.ensureDelayedPCM(frameSamples)
		copy(out, pcm[:frameSamples])
		return out
	}
	if encoderBufferSamples < delaySamples {
		encoderBufferSamples = delaySamples
	}
	if len(e.delayBuffer) != encoderBufferSamples {
		e.delayBuffer = make([]opusRes, encoderBufferSamples)
	}

	tailStart := encoderBufferSamples - delaySamples

	// Preserve the libopus delay-history snapshot window used by CELT transition prefill:
	// delay_buffer[encoder_buffer-delay_comp-Fs/400 : +Fs/400].
	prefillFrameSize := sampleRate / 400
	prefillSamples := prefillFrameSize * channels
	prefillStart := encoderBufferSamples - delaySamples - prefillSamples
	if prefillSamples > 0 && prefillStart >= 0 && prefillStart+prefillSamples <= len(e.delayBuffer) {
		prefill := e.ensureTransitionPrefill(prefillSamples)
		copy(prefill, e.delayBuffer[prefillStart:prefillStart+prefillSamples])
	} else {
		e.scratchTransitionPrefill = e.scratchTransitionPrefill[:0]
	}

	out := e.ensureDelayedPCM(frameSize * channels)
	if frameSamples <= delaySamples {
		copy(out, e.delayBuffer[tailStart:tailStart+frameSamples])
		clear(out[frameSamples:])
	} else {
		copy(out, e.delayBuffer[tailStart:])
		copy(out[delaySamples:], pcm[:frameSamples-delaySamples])
		clear(out[frameSamples:])
	}
	return out
}

// celtTransitionPrefillSource returns libopus tmp_prefill: the Fs/400 samples of
// delay history that precede this frame's CELT input,
// delay_buffer[encoder_buffer-total_buffer-Fs/400 : +Fs/400], copied before the
// delay buffer shifts (src/opus_encoder.c:2297-2301). When this frame ran a SILK
// transition prefill, that prefill's onset ramp has rewritten the same window.
func (e *Encoder) celtTransitionPrefillSource(prefillSamples int) []opusRes {
	src := e.scratchTransitionPrefill
	if e.hasCELTPrefill {
		src = e.scratchCELTPrefill
	}
	if prefillSamples <= 0 || len(src) < prefillSamples {
		return nil
	}
	return src[:prefillSamples]
}

// maybePrefillSILKOnModeTransition stages the SILK prefill of a frame
// (src/opus_encoder.c:1576-1581 and 2191-2209): the prefill of a switch from
// CELT to SILK or Hybrid, where on the first frame of the packet (initSILK) the
// SILK encoder is re-initialized (silk_InitEncoder), and the prefill that
// starts the first frame at a new SILK internal bandwidth (silk_bw_switch). The
// 10 ms of delay history, with the onset ramp that avoids coding a
// discontinuity, is kept for the silk_Encode prefill call that runs once the
// frame's SILK controls are set; captureCELTPrefill also records the part of it
// the CELT transition prefill reads. It runs before the frame advances the
// delay buffer.
func (e *Encoder) maybePrefillSILKOnModeTransition(actualMode Mode, initSILK, captureCELTPrefill bool) {
	fromCELT := e.shouldPrefillSILKOnModeTransition(actualMode)
	bwSwitch := e.silkBWSwitch && actualMode != ModeCELT && !e.lowDelay && !e.restrictedSilkApp
	if !fromCELT && !bwSwitch {
		return
	}
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	// libopus prefill uses encoder_buffer = Fs/100 samples of delay history.
	prefillFrameSize := sampleRate / 100
	prefillSamples := prefillFrameSize * channels
	prefill := e.ensureSilkPrefill(prefillSamples)
	clear(prefill)
	if len(e.delayBuffer) >= prefillSamples {
		copy(prefill, e.delayBuffer[:prefillSamples])
	} else if len(e.delayBuffer) > 0 {
		copy(prefill[prefillSamples-len(e.delayBuffer):], e.delayBuffer)
	}
	e.applySilkTransitionPrefillRamp(prefill, prefillFrameSize)
	e.stageFixedSILKPrefill(captureCELTPrefill)

	if captureCELTPrefill {
		// CELT mode-transition prefill consumes this exact history slice in libopus:
		// delay_buffer[encoder_buffer-delay_comp-Fs/400 : +Fs/400].
		prefillLen := sampleRate / 400
		delayComp := sampleRate / 250
		prefillOffset := prefillFrameSize - delayComp - prefillLen
		celtPrefillSamples := prefillLen * channels
		if prefillLen > 0 && prefillOffset >= 0 && celtPrefillSamples > 0 {
			start := prefillOffset * channels
			end := start + celtPrefillSamples
			if start >= 0 && end <= len(prefill) {
				out := e.ensureCELTPrefill(celtPrefillSamples)
				copy(out, prefill[start:end])
				e.hasCELTPrefill = true
			}
		}
	}

	e.ensureSILKEncoder()
	if fromCELT && initSILK {
		e.silk.Init()
	}
	e.silkPrefillPending = true
}

func (e *Encoder) shouldPrefillSILKOnModeTransition(actualMode Mode) bool {
	if actualMode == ModeCELT || e.lowDelay {
		return false
	}
	prev := e.prevMode
	if !isConcreteMode(prev) || prev != ModeCELT {
		return false
	}
	if e.channels < 1 || e.sampleRate <= 0 {
		return false
	}
	return true
}

// runPendingSILKPrefill runs the staged prefill through silk_Encode with the
// frame's controls and no range coder: prefill 1 after CELT, or 2 at a new
// SILK bandwidth, which keeps the variable LP filter state. The real encode of
// the frame then cannot switch the bandwidth again (opusCanSwitch = 0).
func (e *Encoder) runPendingSILKPrefill(prefill, activity int) error {
	if prefill == 0 {
		return nil
	}
	e.silkPrefillPending = false
	err := e.encodeSILKPrefill(prefill, activity)
	e.silkMode.OpusCanSwitch = false
	return err
}

func (e *Encoder) applySilkTransitionPrefillRamp(prefill []opusRes, prefillFrameSize int) {
	if len(prefill) == 0 || prefillFrameSize <= 0 {
		return
	}
	channels := max(int(e.channels), 1)
	sampleRate := int(e.sampleRate)
	delayComp := sampleRate / 250
	prefillLen := sampleRate / 400
	start := min(max(prefillFrameSize-delayComp-prefillLen, 0), prefillFrameSize)

	prefix := min(start*channels, len(prefill))
	for i := range prefix {
		prefill[i] = 0
	}
	if prefillLen <= 0 {
		return
	}
	if start+prefillLen > prefillFrameSize {
		prefillLen = prefillFrameSize - start
	}
	if prefillLen <= 0 {
		return
	}

	inc := max(48000/sampleRate, 1)
	window := celt.GetWindowBufferF32(prefillLen * inc)
	maxByWindow := prefillLen
	if len(window) > 0 {
		maxByWindow = len(window) / inc
		if maxByWindow < prefillLen {
			prefillLen = maxByWindow
		}
	}
	if prefillLen <= 0 {
		return
	}

	if len(window) == 0 {
		den := opusVal16(prefillLen)
		if den < 1 {
			den = 1
		}
		for i := 0; i < prefillLen; i++ {
			g := opusVal16(i) / den
			base := (start + i) * channels
			for c := 0; c < channels && base+c < len(prefill); c++ {
				prefill[base+c] *= g
			}
		}
		return
	}

	for i := 0; i < prefillLen; i++ {
		w := window[i*inc]
		g := w * w
		base := (start + i) * channels
		for c := 0; c < channels && base+c < len(prefill); c++ {
			prefill[base+c] *= g
		}
	}
}

// updateDelayBuffer advances the delay buffer by the frame, as
// opus_encode_frame_native does once the SILK layer has coded it.
func (e *Encoder) updateDelayBuffer(pcm []opusRes, frameSize int) {
	sampleRate := int(e.sampleRate)
	delayComp := sampleRate / 250
	if delayComp <= 0 {
		return
	}
	channels := max(int(e.channels), 1)
	delaySamples := delayComp * channels
	encoderBufferSamples := (sampleRate / 100) * channels
	frameSamples := min(len(pcm), frameSize*channels)
	if delaySamples <= 0 || frameSamples <= 0 {
		return
	}
	if encoderBufferSamples < delaySamples {
		encoderBufferSamples = delaySamples
	}
	if len(e.delayBuffer) != encoderBufferSamples {
		e.delayBuffer = make([]opusRes, encoderBufferSamples)
	}
	e.updateDelayBufferInternal(pcm, frameSamples, encoderBufferSamples)
}

func (e *Encoder) updateDelayBufferInternal(pcm []opusRes, frameSamples, encoderBufferSamples int) {
	if frameSamples <= 0 || encoderBufferSamples <= 0 {
		return
	}
	if frameSamples >= encoderBufferSamples {
		copy(e.delayBuffer, pcm[frameSamples-encoderBufferSamples:frameSamples])
		return
	}

	keep := encoderBufferSamples - frameSamples
	copy(e.delayBuffer[:keep], e.delayBuffer[frameSamples:frameSamples+keep])
	copy(e.delayBuffer[keep:], pcm[:frameSamples])
}

// selectMode determines the actual encoding mode based on settings and content.
func (e *Encoder) selectMode(frameSize int, signalHint types.Signal) Mode {
	if e.restrictedSilkApp {
		return ModeSILK
	}
	if e.lowDelay {
		return ModeCELT
	}
	if frameSize > e.frame20ms() {
		if e.mode != ModeAuto {
			// Hybrid long packets are encoded as 20ms multi-frame packets.
			if e.mode == ModeHybrid {
				return ModeHybrid
			}
			// CELT 40/60ms is encoded as multi-frame (2/3 x 20ms) packets.
			return e.mode
		}
		bw := e.effectiveBandwidth()

		// Fullband long frames in auto mode follow CELT-only path in libopus audio app.
		if bw == types.BandwidthFullband {
			return ModeCELT
		}
		if bw == types.BandwidthSuperwideband {
			return e.selectLongSWBAutoMode(frameSize, signalHint)
		}
		// Respect explicit or analyzed signal hints.
		switch signalHint {
		case types.SignalVoice:
			// In SWB long-frame auto mode, libopus only uses Hybrid or CELT.
			// Avoid raw SILK packets in this lane.
			if bw == types.BandwidthSuperwideband {
				return ModeHybrid
			}
			return ModeSILK
		case types.SignalMusic:
			return ModeCELT
		}
		// In auto-signal mode for long frames, bias by bandwidth instead of the
		// per-frame classifier to avoid unstable SILK/CELT switching.
		if bw == types.BandwidthSuperwideband {
			return ModeCELT
		}
		return ModeSILK
	}
	if e.mode != ModeAuto {
		return e.mode
	}
	return e.selectShortAutoMode(frameSize, signalHint)
}

func isConcreteMode(mode Mode) bool {
	return mode == ModeSILK || mode == ModeHybrid || mode == ModeCELT
}

// applyCELTTransitionDelay mirrors libopus to_celt handling:
// when switching from SILK/Hybrid to CELT on >=10 ms frames, hold one frame in
// the previous non-CELT mode but advance prev-mode state to CELT for next frame.
func (e *Encoder) applyCELTTransitionDelay(frameSize int, requested Mode) (actual Mode, prevNext Mode) {
	actual = requested
	prevNext = requested

	prev := e.prevMode
	if !isConcreteMode(prev) || !isConcreteMode(requested) {
		return actual, prevNext
	}

	switchingAcrossCELT := (requested == ModeCELT && prev != ModeCELT) ||
		(requested != ModeCELT && prev == ModeCELT)
	if !switchingAcrossCELT {
		return actual, prevNext
	}

	// libopus delays SILK/Hybrid->CELT transition for 10ms+ frames.
	if requested == ModeCELT {
		minDelayFrame := int(e.sampleRate) / 100
		if minDelayFrame <= 0 {
			minDelayFrame = 480
		}
		if frameSize >= minDelayFrame {
			actual = prev
			prevNext = ModeCELT
		}
	}
	return actual, prevNext
}

// selectShortAutoMode ports libopus auto mode-threshold control for 10/20 ms
// frames (SILK/hybrid vs CELT), including previous-mode hysteresis.
func (e *Encoder) selectShortAutoMode(frameSize int, signalHint types.Signal) Mode {
	_ = signalHint
	bw := e.effectiveBandwidth()

	frameRate := int(e.sampleRate) / frameSize
	if frameRate <= 0 {
		frameRate = 50
	}
	useVBR := e.bitrateMode != ModeCBR
	equivRate := e.computeEquivRate(e.bitrate, e.channels, int32(frameRate), useVBR, ModeAuto, e.complexity, e.packetLoss)

	prev := e.prevAutoMode
	if prev != ModeSILK && prev != ModeHybrid && prev != ModeCELT {
		prev = ModeAuto
	}

	voiceEst := e.autoVoiceEstimate(prev)
	modeVoice := 64000
	if e.channels == 2 {
		modeVoice = 44000
	}
	const modeMusic = 10000
	threshold := modeMusic + (voiceEst*voiceEst*(modeVoice-modeMusic))/16384
	if e.voipApp {
		threshold += 8000
	}
	switch prev {
	case ModeCELT:
		threshold -= 4000
	case ModeSILK, ModeHybrid:
		threshold += 4000
	}

	mode := ModeSILK
	if equivRate >= int32(threshold) {
		mode = ModeCELT
	}
	// Match libopus behavior: with in-band FEC and sufficient expected loss,
	// force SILK unless music-safe FEC is confident the signal is music.
	if e.fecEnabled && e.packetLoss > int32((128-voiceEst)>>4) &&
		(e.fecConfig != InBandFECMusicSafe || voiceEst > 25) {
		mode = ModeSILK
	}
	// Match libopus behavior: when DTX is enabled for voiced content, favor SILK.
	if e.dtxEnabled && voiceEst > 100 {
		mode = ModeSILK
	}
	// For SWB/FB lanes, SILK-only maps to hybrid in libopus.
	if mode == ModeSILK && bw > types.BandwidthWideband {
		mode = ModeHybrid
	}
	if mode == ModeHybrid && bw <= types.BandwidthWideband {
		mode = ModeSILK
	}

	if !ValidFrameSize(frameSize, mode) {
		if ValidFrameSize(frameSize, ModeCELT) {
			return ModeCELT
		}
		if ValidFrameSize(frameSize, ModeSILK) {
			return ModeSILK
		}
		return ModeCELT
	}
	return mode
}

// selectLongSWBAutoMode mirrors libopus mode-threshold control for long-frame SWB
// auto mode (Celt-only vs Silk/Hybrid lane), using analysis-derived voice estimate
// and previous-mode hysteresis.
func (e *Encoder) selectLongSWBAutoMode(frameSize int, signalHint types.Signal) Mode {
	_ = signalHint
	frameRate := int(e.sampleRate) / frameSize
	if frameRate <= 0 {
		frameRate = 50
	}
	useVBR := e.bitrateMode != ModeCBR
	equivRate := e.computeEquivRate(e.bitrate, e.channels, int32(frameRate), useVBR, ModeAuto, e.complexity, e.packetLoss)

	prev := e.prevAutoMode
	if prev != ModeCELT && prev != ModeSILK && prev != ModeHybrid {
		prev = ModeAuto
	}
	voiceEst := e.autoVoiceEstimate(prev)

	modeVoice := 64000
	if e.channels == 2 {
		modeVoice = 44000
	}
	const modeMusic = 10000
	threshold := modeMusic + (voiceEst*voiceEst*(modeVoice-modeMusic))/16384

	// Match libopus auto-mode threshold bias for VoIP.
	if e.voipApp {
		threshold += 8000
	}

	// libopus hysteresis: bias against rapid CELT<->SILK/HYBRID switching.
	switch prev {
	case ModeCELT:
		threshold -= 4000
	case ModeSILK, ModeHybrid:
		threshold += 4000
	}

	mode := ModeHybrid
	if equivRate >= int32(threshold) {
		mode = ModeCELT
	}
	// Match libopus behavior: with in-band FEC and sufficient expected loss,
	// force SILK unless music-safe FEC is confident the signal is music.
	if e.fecEnabled && e.packetLoss > int32((128-voiceEst)>>4) &&
		(e.fecConfig != InBandFECMusicSafe || voiceEst > 25) {
		mode = ModeHybrid
	}
	// Match libopus behavior: when DTX is enabled for voiced content, favor SILK lane.
	if e.dtxEnabled && voiceEst > 100 {
		mode = ModeHybrid
	}
	return mode
}

// autoSignalFromPCM runs analysis when needed and returns a voice or music hint
// only when a valid long-frame result crosses a confidence threshold. Otherwise
// it returns SignalAuto.
func (e *Encoder) autoSignalFromPCM(pcm []opusRes, frameSize int) types.Signal {
	if len(pcm) == 0 || frameSize <= 0 {
		return types.SignalAuto
	}
	if !e.analysisEnabled() {
		return types.SignalAuto
	}
	if !e.lastAnalysisFresh {
		pcm32 := []float32(pcm)
		f20 := e.frame20ms()
		runAnalyzer := frameSize > f20
		if !runAnalyzer && e.mode == ModeAuto && frameSize == f20 && e.effectiveBandwidth() == types.BandwidthSuperwideband {
			runAnalyzer = true
		}
		if runAnalyzer && e.analyzer != nil {
			info := e.analyzer.RunAnalysis(pcm32, frameSize, int(e.channels))
			if info.Valid {
				e.lastAnalysisInfo = info
				e.lastAnalysisValid = true
				e.lastAnalysisFresh = true
			}
		}
	}

	// Only trust clear decisions from analysis probabilities on long frames.
	if frameSize > e.frame20ms() && e.lastAnalysisValid {
		if e.lastAnalysisInfo.MusicProb >= 0.65 {
			return types.SignalMusic
		}
		if e.lastAnalysisInfo.MusicProb <= 0.60 {
			return types.SignalVoice
		}
		return types.SignalAuto
	}
	// libopus mode-auto fallback when analysis is unavailable/invalid is to keep
	// OPUS_SIGNAL_AUTO and rely on threshold control with default voice estimate.
	return types.SignalAuto
}

func (e *Encoder) autoVoiceEstimate(prev Mode) int {
	voiceEst := 48 // OPUS_APPLICATION_AUDIO fallback.
	if e.voipApp {
		voiceEst = 115
	}
	if e.signalType == types.SignalVoice {
		return 127
	}
	if e.signalType == types.SignalMusic {
		return 0
	}
	if !e.lastAnalysisValid {
		return voiceEst
	}
	prob := e.lastAnalysisInfo.MusicProb
	switch prev {
	case ModeCELT:
		prob = e.lastAnalysisInfo.MusicProbMax
	case ModeSILK, ModeHybrid:
		prob = e.lastAnalysisInfo.MusicProbMin
	}
	if prob < 0 {
		prob = 0
	}
	if prob > 1 {
		prob = 1
	}
	voiceRatio := int(opusmath.FloorHalfPlusF32ToInt32(float32(100) * (float32(1) - prob)))
	voiceEst = min(
		// OPUS_APPLICATION_AUDIO clamp.
		(voiceRatio*327)>>8, 115)
	return voiceEst
}

// effectiveBandwidth returns the resolved bandwidth for encoder submodules.
func (e *Encoder) effectiveBandwidth() types.Bandwidth {
	if e.lfe {
		return types.BandwidthNarrowband
	}
	return e.bandwidth
}

// packetTOCFrameSize maps the native-Fs per-frame size to the 48 kHz-equivalent
// count used to index the TOC config table. libopus gen_toc derives the TOC
// period from Fs/frame_size, so the duration (and therefore the period) is
// identical to the 48 kHz frame size frameSize*48000/Fs. At 48 kHz it is the
// identity; at sub-48 kHz native rates it scales the native frame size up to its
// 48 kHz-equivalent duration so the on-wire TOC config is unchanged.
func (e *Encoder) packetTOCFrameSize(frameSize int) int {
	fs := int(e.sampleRate)
	if fs <= 0 || fs == 48000 {
		return frameSize
	}
	return frameSize * 48000 / fs
}

func (e *Encoder) celtPredictionMode() int {
	if e.predictionDisabled {
		return 0
	}
	return 2
}

// silkInternalBandwidth is the TOC bandwidth of a SILK-only frame coded at
// the SILK internal sampling rate (src/opus_encoder.c:2215-2227).
func silkInternalBandwidth(internalSampleRate int32) types.Bandwidth {
	switch internalSampleRate {
	case 8000:
		return types.BandwidthNarrowband
	case 12000:
		return types.BandwidthMediumband
	default:
		return types.BandwidthWideband
	}
}

// configureSILKMode sets the silk_mode controls opus_encode_frame_native hands
// silk_Encode for a frame of mode (src/opus_encoder.c:2050-2189): the SILK
// rate, the packet duration and channel layout, the internal sampling rate
// request and limits, the bit cap and CBR flag, and the FEC, complexity, loss
// and dependency settings. maxDataBytes is the frame's max_data_bytes after
// the 1276-byte cap: a SILK-only frame whose budget cannot carry wideband caps
// the internal rate.
func (e *Encoder) configureSILKMode(mode Mode, frameSize, maxDataBytes, bitRate, maxBits int, useCBR bool) {
	m := &e.silkMode
	sampleRate := int(e.sampleRate)
	m.NChannelsAPI = e.channels
	m.NChannelsInternal = int32(e.silkInternalChannels())
	m.APISampleRate = e.sampleRate
	m.PayloadSizeMs = int32(1000 * frameSize / sampleRate)
	m.BitRate = int32(bitRate)
	switch e.effectiveBandwidth() {
	case types.BandwidthNarrowband:
		m.DesiredInternalSampleRate = 8000
	case types.BandwidthMediumband:
		m.DesiredInternalSampleRate = 12000
	default:
		m.DesiredInternalSampleRate = 16000
	}
	// Hybrid frames do not allow a bandwidth reduction at the lowest rates.
	m.MinInternalSampleRate = 8000
	if mode == ModeHybrid {
		m.MinInternalSampleRate = 16000
	}
	m.MaxInternalSampleRate = 16000
	if mode == ModeSILK {
		effectiveMaxRate := int32(bitsToBitrateFs(maxDataBytes*8, sampleRate, frameSize))
		if sampleRate/frameSize > 50 {
			effectiveMaxRate = effectiveMaxRate * 2 / 3
		}
		if effectiveMaxRate < 8000 {
			m.MaxInternalSampleRate = 12000
			m.DesiredInternalSampleRate = min(12000, m.DesiredInternalSampleRate)
		}
		if effectiveMaxRate < 7000 {
			m.MaxInternalSampleRate = 8000
			m.DesiredInternalSampleRate = min(8000, m.DesiredInternalSampleRate)
		}
		// At 96 kHz no input resampler serves 8 or 12 kHz.
		if sampleRate == 96000 {
			m.MaxInternalSampleRate = 16000
			m.DesiredInternalSampleRate = 16000
		}
	}
	m.PacketLossPercentage = e.packetLoss
	m.Complexity = e.complexity
	m.LBRRCoded = e.lbrrCoded
	m.UseCBR = useCBR
	m.MaxBits = int32(maxBits)
	m.ToMono = e.toMono != 0
	m.ReducedDependency = e.predictionDisabled
}

// silkActivity returns the Opus-level voice activity decision handed to
// silk_Encode (src/opus_encoder.c:1888-1930): VAD_NO_DECISION when the analysis
// made no decision, otherwise whether the frame is active.
func (e *Encoder) silkActivity() int {
	switch {
	case !e.lastOpusVADValid:
		return silk.VADNoDecision
	case e.lastOpusVADActive:
		return 1
	default:
		return silk.VADNoActivity
	}
}

// maxLongPacketFrameBytes bounds the combined size of all subframe payloads
// kept by keepFrame within a single long packet. Each of the <=6 internal
// subframes is independently capped by at most a full Opus packet, so this
// covers any realisable sum while letting resetPacketFrameScratch pre-grow the
// backing buffer once, keeping earlier keepFrame subslices stable.
const maxLongPacketFrameBytes = 6 * maxSilkPacketBytes

// resetPacketFrameScratch prepares the reusable long-packet assembly scratch at
// the top of each long-packet encode. It pre-grows scratchFrameBytes so that the
// per-frame keepFrame appends never reallocate (which would invalidate earlier
// returned subslices).
func (e *Encoder) resetPacketFrameScratch() {
	if cap(e.scratchFrameBytes) < maxLongPacketFrameBytes {
		e.scratchFrameBytes = make([]byte, 0, maxLongPacketFrameBytes)
	}
	e.scratchFrameBytes = e.scratchFrameBytes[:0]
	if cap(e.scratchQEXTPayloadBytes) < maxLongPacketFrameBytes {
		e.scratchQEXTPayloadBytes = make([]byte, 0, maxLongPacketFrameBytes)
	}
	e.scratchQEXTPayloadBytes = e.scratchQEXTPayloadBytes[:0]
}

// ensurePacketScratch reserves n bytes for an assembled packet. The default
// buffer holds a TOC byte and a 1275-byte frame. Longer packets use the caller's
// larger byte budget, matching libopus's assembly into the caller's buffer.
func (e *Encoder) ensurePacketScratch(n int) {
	if cap(e.scratchPacket) >= n {
		e.scratchPacket = e.scratchPacket[:cap(e.scratchPacket)]
		return
	}
	e.scratchPacket = make([]byte, n)
}

// keepFrame copies frame into the reusable scratchFrameBytes backing buffer and
// returns a length-capped subslice. The range coder output buffer is reused
// across subframes, so the copy gives each kept frame stable storage. The
// returned subslice is 3-index sliced so a stray append cannot reach into the
// next frame's bytes.
func (e *Encoder) keepFrame(frame []byte) []byte {
	start := len(e.scratchFrameBytes)
	e.scratchFrameBytes = append(e.scratchFrameBytes, frame...)
	end := len(e.scratchFrameBytes)
	return e.scratchFrameBytes[start:end:end]
}

// keepQEXTPayload copies a QEXT extension payload into the reusable
// scratchQEXTPayloadBytes backing buffer, giving it stable storage for the
// duration of packet assembly without a per-frame heap allocation.
func (e *Encoder) keepQEXTPayload(payload []byte) []byte {
	start := len(e.scratchQEXTPayloadBytes)
	e.scratchQEXTPayloadBytes = append(e.scratchQEXTPayloadBytes, payload...)
	end := len(e.scratchQEXTPayloadBytes)
	return e.scratchQEXTPayloadBytes[start:end:end]
}

// multiFramePacket carries the packet-level arguments of the multi-frame
// branch of opus_encode_native (src/opus_encoder.c:1697-1838).
type multiFramePacket struct {
	mode             Mode
	frameSize        int
	originalBitrate  int // st->bitrate_bps before the DRED reservation
	encodingBitrate  int // st->bitrate_bps after the DRED reservation
	dredBitrate      int
	dredExtraDelay   int
	outDataBytes     int
	equivRate        int32
	redundancy       bool
	celtToSILK       bool
	toCELT           bool
	floatInputDirect bool
}

// encodeMultiFramePacket codes a packet longer than one Opus frame the way
// opus_encode_native does (src/opus_encoder.c:1697-1838): CELT-only and hybrid
// packets split into 20 ms frames, SILK-only packets into 40 ms (80 ms), 60 ms
// (120 ms) or 20 ms (100 ms) frames. Each frame runs opus_encode_frame_native
// with the packet's equiv_rate, redundancy and prefill and a budget curr_max,
// and the repacketizer joins them.
func (e *Encoder) encodeMultiFramePacket(pcm, vadPCM []opusRes, p multiFramePacket) ([]byte, error) {
	mode := p.mode
	frameSize := p.frameSize
	f20 := e.frame20ms()
	encFrameSize := f20
	if mode == ModeSILK {
		switch frameSize {
		case 4 * f20: // 80 ms -> 2x40 ms
			encFrameSize = 2 * f20
		case 6 * f20: // 120 ms -> 2x60 ms
			encFrameSize = 3 * f20
		}
	}
	if frameSize <= encFrameSize || frameSize%encFrameSize != 0 {
		return nil, ErrInvalidFrameSize
	}
	frameCount := frameSize / encFrameSize
	if frameCount > 6 {
		return nil, ErrInvalidFrameSize
	}
	channels := int(e.channels)
	if len(pcm) != frameSize*channels || len(vadPCM) != frameSize*channels {
		return nil, ErrInvalidFrameSize
	}
	// The TOC codes the 48 kHz-equivalent frame duration.
	tocFrameSize := encFrameSize * 48000 / int(e.sampleRate)
	if e.analysisReadBakSet && e.analyzer != nil {
		e.analyzer.ReadPos = e.analysisReadPosBak
		e.analyzer.ReadSubframe = e.analysisSubframeBak
	}

	e.resetPacketFrameScratch()
	frames := e.scratchFrameSlots[:frameCount]
	sameSize := true
	prevSize := -1
	// The repacketizer takes the whole output buffer in VBR and the CBR
	// packet size in CBR.
	repacketizeLen := p.outDataBytes
	if e.bitrateMode == ModeCBR {
		repacketizeLen = min(e.targetBytesForBitrate(p.originalBitrate, frameSize), p.outDataBytes)
	}
	repacketizeLen = max(repacketizeLen, 1)
	// Worst cases: code 2 with different sizes for 2 frames, code 3 VBR for
	// more.
	maxHeaderBytes := 3
	if frameCount > 2 {
		maxHeaderBytes = 2 + (frameCount-1)*2
	}
	qext := extsupport.QEXT && mode == ModeCELT && e.qextActive()
	if qext {
		// The separators and the padding length byte of the QEXT extensions.
		maxHeaderBytes += frameCount
	}
	maxLenSum := max(frameCount+repacketizeLen-maxHeaderBytes, frameCount)
	// The assembled packet is bounded by maxLenSum+maxHeaderBytes.
	e.ensurePacketScratch(maxLenSum + maxHeaderBytes)
	dredBytes := 0
	if p.dredBitrate > 0 {
		dredBytes = bitrateToBitsFs(p.dredBitrate, int(e.sampleRate), frameSize) / 8
	}
	dredActive := e.dredEncodingActive()
	if dredActive {
		e.clearDREDPacketSnapshot()
	}
	totSize := 0
	firstFrameMaxBytes := 0
	var qextExtensions [6]packetExtension
	qextExtensionCount := 0
	savedBitrate := e.bitrate
	e.bitrate = int32(p.encodingBitrate)
	defer func() { e.bitrate = savedBitrate }()
	bakToMono := e.beginMultiFramePacket()
	defer func() { e.toMono = bakToMono }()
	// st->prev_mode as each frame sees it: the frames of the packet advance
	// it once they are coded.
	prevMode := e.prevMode
	var packetBW types.Bandwidth
	frameStride := encFrameSize * channels
	for i := range frameCount {
		e.primeSubframeAnalysis(encFrameSize)
		start := i * frameStride
		rawSubPCM := pcm[start : start+frameStride]
		floatOffset := -1
		if p.floatInputDirect {
			floatOffset = start
		}
		subPCM := e.preprocessInputHPFrame(rawSubPCM, encFrameSize, mode, floatOffset)
		e.preprocessFixedInputRes(encFrameSize)
		subVADPCM := vadPCM[start : start+frameStride]
		e.nonfinalFrame = i < frameCount-1
		if mode != ModeCELT && i > 0 {
			// The packet's SILK prefill reruns in every frame from the delay
			// history the previous frame left (src/opus_encoder.c:2191-2209).
			e.maybePrefillSILKOnModeTransition(mode, false, false)
		}
		e.updateFrameActivity(subVADPCM, isDigitalSilenceRes(subVADPCM, e.lsbDepth), mode)
		dredNoDecision := !e.lastOpusVADValid
		if dredActive {
			e.processDREDLatentsWithActivity(subPCM, p.dredExtraDelay, e.lastOpusVADActive)
			if mode == ModeCELT && i == 0 {
				e.snapshotDREDPacketState()
			}
		}

		currMax := min(bitrateToBitsFs(p.encodingBitrate, int(e.sampleRate), encFrameSize)/8, maxLenSum/frameCount)
		if dredBytes > 0 {
			currMax = min(currMax, (maxLenSum-dredBytes)/frameCount)
			if i == 0 {
				currMax += dredBytes
			}
		}
		// Each QEXT subframe reserves its extension-length signal before coding.
		// src/opus_encoder.c reduces curr_max by curr_max/254 in this loop.
		if e.qextActive() {
			currMax -= currMax / 254
		}
		currMax = min(maxLenSum-totSize, currMax)
		if i == 0 {
			firstFrameMaxBytes = currMax
		}
		// A switch to CELT is signalled in the last frame, a switch from CELT
		// in the first one.
		frameToCELT := p.toCELT && i == frameCount-1
		frameRedundancy := p.redundancy && (frameToCELT || (!p.toCELT && i == 0))
		frame, err := e.encodeFrameNative(subPCM, frameRequest{
			mode:         mode,
			frameSize:    encFrameSize,
			maxDataBytes: currMax,
			dredBitrate:  p.dredBitrate,
			equivRate:    p.equivRate,
			prevMode:     prevMode,
			redundancy:   frameRedundancy,
			celtToSILK:   p.celtToSILK,
		})
		if err != nil {
			return nil, err
		}
		if frame.dtx {
			// SILK DTX still consumes and high-pass filters this child input,
			// but libopus returns before advancing the delay buffer.
			e.advanceFixedInputCursor(encFrameSize)
		}
		// The repacketizer only joins frames with the same TOC.
		if i == 0 {
			packetBW = frame.bw
		} else if frame.bw != packetBW {
			return nil, ErrEncodingFailed
		}
		if mode != ModeCELT {
			if dredActive && dredNoDecision {
				e.backfillDREDActivityForFrame(encFrameSize, e.silkMode.SignalType != 0)
			}
			if dredActive && i == 0 {
				e.snapshotDREDPacketState()
			}
		}
		// Keep a stable copy: the frame scratch is reused.
		frameCopy := e.keepFrame(frame.data)
		// A SILK DTX frame is TOC-only and ends before the Opus-level DTX
		// decision; any other frame runs decide_dtx_mode, and a suppressed
		// frame becomes a length-0 frame of the packet.
		suppressed := frame.dtx
		if !frame.dtx {
			prevMode = mode
			if frameToCELT {
				prevMode = ModeCELT
			}
			if mode != ModeCELT {
				e.commitMultiFrameSubframe(i == frameCount-1)
			}
			suppressed = !dredActive && e.subframeDTXSuppress(encFrameSize)
		}
		e.multiFrameLastSubframeDTX = suppressed
		if suppressed {
			frameCopy = frameCopy[:0]
			e.multiFrameDTXCount++
			totSize++ // tot_size += tmp_len (1) for a DTX frame
		} else {
			totSize += len(frameCopy) + 1
		}
		frames[i] = frameCopy
		if !suppressed && qext {
			if qextPayload := e.lastQEXTPayload(); len(qextPayload) > 0 {
				qextExtensions[qextExtensionCount] = packetExtension{
					ID:    qextExtensionID,
					Data:  e.keepQEXTPayload(qextPayload),
					Frame: i,
				}
				qextExtensionCount++
			}
		}
		if prevSize >= 0 && len(frameCopy) != prevSize {
			sameSize = false
		}
		prevSize = len(frameCopy)
	}
	e.analysisReadBakSet = false

	stereo := e.packetStereoForMode(mode)
	if dredActive {
		// DRED plan and extension sizing use the original packet budget. The
		// primary frames above use p.encodingBitrate after reservation.
		e.bitrate = int32(p.originalBitrate)
		if dredPacket, ok, err := e.maybeBuildMultiFrameDREDPacket(frames, mode, packetBW, frameSize, tocFrameSize, firstFrameMaxBytes, stereo, !sameSize, qextExtensions[:qextExtensionCount]); err != nil {
			return nil, err
		} else if ok {
			return dredPacket, nil
		}
	}
	var packetLen int
	var err error
	if qextExtensionCount > 0 {
		// opus_repacketizer_out_range_impl pads CBR multi-frame packets to
		// repacketize_len while retaining each frame's QEXT extension.
		withPadding := e.bitrateMode == ModeCBR && e.multiFrameDTXCount != frameCount
		targetLen := 0
		if withPadding {
			targetLen = repacketizeLen
		}
		packetLen, err = buildMultiFramePacketWithExtensionsInto(e.scratchPacket, frames, modeToTypes(mode), packetBW, tocFrameSize, stereo, !sameSize, qextExtensions[:qextExtensionCount], targetLen, withPadding)
	} else {
		packetLen, err = buildMultiFramePacketInto(e.scratchPacket, frames, modeToTypes(mode), packetBW, tocFrameSize, stereo, !sameSize)
	}
	if err != nil {
		return nil, err
	}
	return e.scratchPacket[:packetLen], nil
}

// beginMultiFramePacket runs the packet start of the multi-frame branch of
// opus_encode_native (src/opus_encoder.c:1763-1767): a packet that carries a
// stereo->mono transition (silk_mode.toMono) forces mono coding from then on
// (force_channels = 1); otherwise prev_channels takes the packet's channels.
// Every sub-frame is coded with silk_mode.toMono = 0; the packet's value, which
// comes back afterwards, is returned.
func (e *Encoder) beginMultiFramePacket() (bakToMono int32) {
	bakToMono = e.toMono
	if bakToMono != 0 {
		e.forceChannels = 1
	} else {
		e.prevChannels = e.streamChannels
	}
	e.toMono = 0
	e.multiFrameCommitted = false
	e.multiFrameLastCommitted = false
	return bakToMono
}

// commitMultiFrameSubframe records that a SILK or Hybrid sub-frame reached the
// end-of-frame bookkeeping of opus_encode_frame_native (the previous mode,
// prev_channels and st->first), which a SILK DTX sub-frame skips.
func (e *Encoder) commitMultiFrameSubframe(last bool) {
	e.multiFrameCommitted = true
	e.multiFrameLastCommitted = last
}

// ensureSILKEncoder creates the SILK encoder on first use, in the state
// silk_InitEncoder leaves it (src/opus_encoder.c opus_encoder_init). The SILK
// encoder picks and changes its internal sampling rate itself from the
// silk_mode controls of each frame.
func (e *Encoder) ensureSILKEncoder() {
	if e.silk == nil {
		e.silk = silk.NewPacketEncoder(int(e.channels))
	}
}

// trackPeakSignalEnergy ports the peak signal energy tracking of
// opus_encode_native (src/opus_encoder.c:1310-1318): once per packet, on the
// input of the whole packet, unless it is digital silence or the packet's
// analysis finds it inactive.
func (e *Encoder) trackPeakSignalEnergy(pcm []opusRes, isSilence bool) {
	if isSilence || e.dtx == nil {
		return
	}
	if !e.lastAnalysisValid || e.lastAnalysisInfo.VADProb > DTXActivityThreshold {
		e.dtx.peakSignalEnergy = maxf(0.999*e.dtx.peakSignalEnergy, computeFrameEnergyRes(pcm))
	}
}

// updateFrameActivity ports the Opus-level voice activity decision of
// opus_encode_frame_native (src/opus_encoder.c:1911-1930) for a frame whose
// input is pcm: inactive for digital silence; otherwise the frame's analysis
// decides, and a frame it finds inactive stays active when it is loud against
// the tracked peak; without analysis a CELT-only frame compares its energy
// with the peak, and any other frame makes no decision (VAD_NO_DECISION).
func (e *Encoder) updateFrameActivity(pcm []opusRes, isSilence bool, mode Mode) {
	e.lastOpusVADActivityObserved = true
	e.lastAnalysisFresh = false
	peak := opusVal32(0)
	if e.dtx != nil {
		peak = e.dtx.peakSignalEnergy
	}
	switch {
	case isSilence:
		e.lastOpusVADProb = 0
		e.lastOpusVADValid = true
		e.lastOpusVADActive = false
	case e.lastAnalysisValid:
		e.lastOpusVADProb = e.lastAnalysisInfo.VADProb
		e.lastOpusVADValid = true
		e.lastOpusVADActive = e.lastOpusVADProb >= DTXActivityThreshold ||
			peak < pseudoSNRThreshold*computeFrameEnergyRes(pcm)
	case mode == ModeCELT:
		// Peak energy is boosted a bit because not only the active frames
		// are averaged.
		e.lastOpusVADProb = 1
		e.lastOpusVADValid = true
		e.lastOpusVADActive = peak < pseudoSNRThreshold*(0.5*computeFrameEnergyRes(pcm))
	default:
		e.clearOpusVADDecision()
	}
}

func (e *Encoder) clearOpusVADDecision() {
	e.lastOpusVADValid = false
	e.lastOpusVADActive = true
	e.lastOpusVADProb = 1.0
}

// resolveDTXActivity resolves the libopus opus_int activity for the just-encoded
// frame (opus_encoder.c:2235). When the Opus-level VAD made no decision
// (VAD_NO_DECISION, lastOpusVADValid==false) libopus resolves activity from the
// SILK signal type: activity = (signalType != TYPE_NO_VOICE_ACTIVITY).
func (e *Encoder) resolveDTXActivity() bool {
	if e.lastOpusVADValid {
		return e.lastOpusVADActive
	}
	if e.silk != nil {
		return e.silkMode.SignalType != 0
	}
	// VAD_NO_DECISION with no SILK result resolves to active (true), matching the
	// libopus default where activity stays VAD_NO_DECISION (-1, truthy) and
	// decide_dtx_mode treats !activity as false.
	return true
}

// celtUpsampleFactor mirrors libopus resampling_factor(Fs): the CELT input
// upsample factor for the native API rate (1 at 48 kHz, 2/3/4/6 at
// 24/16/12/8 kHz). The float CELT encoder consumes native-Fs frame sizes and
// upsamples to the 48 kHz core internally.
func (e *Encoder) celtUpsampleFactor() int {
	switch e.sampleRate {
	case 24000:
		return 2
	case 16000:
		return 3
	case 12000:
		return 4
	case 8000:
		return 6
	}
	return 1
}

// ensureCELTEncoder creates the CELT encoder on first use and syncs the
// Opus-level CELT settings. The CELT prediction setting is frame state: the
// frame encode sets it for CELT-only and hybrid frames, after a mode-transition
// prefill and before a SILK->CELT redundant frame, and the CELT->SILK redundant
// frame of a SILK-only frame codes with the setting the last CELT use left
// (src/opus_encoder.c:2288-2295, 2478-2486, 2519-2526).
func (e *Encoder) ensureCELTEncoder() {
	if e.celtEncoder == nil {
		e.celtEncoder = celt.NewEncoder(int(e.channels))
		configureCELTEncoderForSampleRate(e.celtEncoder, e.sampleRate)
		e.celtEncoder.SetComplexity(int(e.complexity))
		// Opus encoder already rounds input to the configured LSB depth.
		e.celtEncoder.SetLSBQuantizationEnabled(false)
		// Opus encoder already applies dc_reject at the top level.
		e.celtEncoder.SetDCRejectEnabled(false)
		// Opus encoder already applies CELT delay compensation at the top level.
		e.celtEncoder.SetDelayCompensationEnabled(false)
		e.celtEncoder.SetPhaseInversionDisabled(e.phaseInversionDisabled)
		// celt_encoder_init leaves CBR at OPUS_BITRATE_MAX with the VBR
		// constraint on.
		e.celtEncoder.SetVBR(false)
		e.celtEncoder.SetConstrainedVBR(true)
		e.celtEncoder.SetBitrate(celt.BitrateMax)
	}
	e.syncQEXTToCELT()
	e.celtEncoder.SetLFE(e.lfe)
	e.syncCELTEnergyMask()
	e.celtEncoder.SetStreamChannels(int(e.streamChannels))
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	e.celtEncoder.SetPacketLoss(int(e.packetLoss))
	// Default the CELT input upsample to the API rate's resampling_factor so the
	// CELT prefill (Fs/400 native) and CELT-only frames consume native-Fs sizes.
	// The redundancy CELT path overrides this to 1 (fixed 48 kHz block).
	e.celtEncoder.SetUpsample(e.celtUpsampleFactor())
}

// ValidFrameSize returns true if the frame size is valid for the given mode.
func ValidFrameSize(frameSize int, mode Mode) bool {
	switch mode {
	case ModeSILK:
		return frameSize == 480 || frameSize == 960 || frameSize == 1920 ||
			frameSize == 2880 || frameSize == 3840 || frameSize == 4800 || frameSize == 5760
	case ModeHybrid:
		return frameSize == 480 || frameSize == 960 || frameSize == 1920 ||
			frameSize == 2880 || frameSize == 3840 || frameSize == 4800 || frameSize == 5760
	case ModeCELT:
		return frameSize == 120 || frameSize == 240 || frameSize == 480 ||
			frameSize == 960 || frameSize == 1920 || frameSize == 2880 ||
			frameSize == 3840 || frameSize == 4800 || frameSize == 5760
	default:
		return frameSize == 120 || frameSize == 240 || frameSize == 480 ||
			frameSize == 960 || frameSize == 1920 || frameSize == 2880 ||
			frameSize == 3840 || frameSize == 4800 || frameSize == 5760
	}
}

// SetSignalType sets the signal type hint for mode selection.
func (e *Encoder) SetSignalType(signal types.Signal) {
	e.signalType = signal
}

// SignalType returns the current signal type hint.
func (e *Encoder) SignalType() types.Signal {
	return e.signalType
}

// LastOpusVADProb returns the last Opus-level VAD probability (0..1).
func (e *Encoder) LastOpusVADProb() float32 {
	return e.lastOpusVADProb
}

// LastOpusVADActive returns whether the Opus-level VAD classified the last frame as active.
func (e *Encoder) LastOpusVADActive() bool {
	return e.lastOpusVADActive
}

// SetMaxBandwidth sets the maximum bandwidth limit.
func (e *Encoder) SetMaxBandwidth(bw types.Bandwidth) {
	e.maxBandwidth = bw
	if e.celtEncoder != nil {
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	}
}

// MaxBandwidth returns the maximum bandwidth limit.
func (e *Encoder) MaxBandwidth() types.Bandwidth {
	return e.maxBandwidth
}

// SetForceChannels sets the forced channel count.
func (e *Encoder) SetForceChannels(channels int) {
	e.forceChannels = int32(channels)
}

// ForceChannels returns the forced channel count (-1 = auto).
func (e *Encoder) ForceChannels() int {
	return int(e.forceChannels)
}

// SetLFE enables or disables LFE mode.
func (e *Encoder) SetLFE(enabled bool) {
	e.lfe = enabled
	e.setFixedCELTLFE(enabled)
	if e.celtEncoder != nil {
		e.celtEncoder.SetLFE(enabled)
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	}
}

// LFE reports whether LFE mode is enabled.
func (e *Encoder) LFE() bool {
	return e.lfe
}

// Lookahead returns the encoder's algorithmic delay in samples at its
// configured input sample rate.
func (e *Encoder) Lookahead() int {
	baseLookahead := int(e.sampleRate) / 400
	if e.lowDelay {
		return baseLookahead
	}
	delayComp := int(e.sampleRate) / 250
	return baseLookahead + delayComp
}

// SetLSBDepth sets the input signal's LSB depth (8-24 bits).
func (e *Encoder) SetLSBDepth(depth int) {
	if depth < 8 {
		depth = 8
	}
	if depth > 24 {
		depth = 24
	}
	e.lsbDepth = int32(depth)
	if e.analyzer != nil {
		e.analyzer.SetLSBDepth(depth)
	}
}

// LSBDepth returns the current LSB depth setting.
func (e *Encoder) LSBDepth() int {
	return int(e.lsbDepth)
}

// SetFloatInputFrame exposes the current public float32 frame to the encoder hot
// path so analysis can consume it directly and 24-bit quantization can skip a
// no-op round-trip.
func (e *Encoder) SetFloatInputFrame(pcm []float32) {
	e.floatInputFrame = pcm
	e.floatInputExact = pcm != nil
}

// ClearFloatInputFrame clears the per-call float32 input override.
func (e *Encoder) ClearFloatInputFrame() {
	e.floatInputFrame = nil
	e.floatInputExact = false
}

// SetPredictionDisabled disables inter-frame prediction
// (silk_mode.reducedDependency). CELT picks it up at the next CELT or hybrid
// frame (src/opus_encoder.c:2288-2295).
func (e *Encoder) SetPredictionDisabled(disabled bool) {
	e.predictionDisabled = disabled
}

// PredictionDisabled returns whether inter-frame prediction is disabled.
func (e *Encoder) PredictionDisabled() bool {
	return e.predictionDisabled
}

// SetPhaseInversionDisabled disables stereo phase inversion.
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	if e.restrictedSilkApp {
		return
	}
	e.phaseInversionDisabled = disabled
	if e.celtEncoder != nil {
		e.celtEncoder.SetPhaseInversionDisabled(disabled)
	}
}

// PhaseInversionDisabled returns whether stereo phase inversion is disabled.
func (e *Encoder) PhaseInversionDisabled() bool {
	if e.restrictedSilkApp {
		return false
	}
	return e.phaseInversionDisabled
}

// SetCELTEnergyMask sets per-band CELT surround masking (21 mono, 42 stereo).
func (e *Encoder) SetCELTEnergyMask(mask []float32) {
	needed := celt.MaxBands * int(e.channels)
	if needed <= 0 || len(mask) < needed {
		if len(e.celtEnergyMask) > 0 {
			clear(e.celtEnergyMask)
			e.celtEnergyMask = e.celtEnergyMask[:0]
		}
		if e.celtEncoder != nil {
			e.celtEncoder.SetEnergyMask(nil)
		}
		e.syncFixedCELTEnergyMask()
		return
	}
	if cap(e.celtEnergyMask) < needed {
		e.celtEnergyMask = make([]float32, needed)
	} else {
		e.celtEnergyMask = e.celtEnergyMask[:needed]
	}
	copy(e.celtEnergyMask, mask[:needed])
	e.syncCELTEnergyMask()
	e.syncFixedCELTEnergyMask()
}

// CELTEnergyMask returns the current CELT energy mask.
func (e *Encoder) CELTEnergyMask() []float32 {
	out := make([]float32, len(e.celtEnergyMask))
	copy(out, e.celtEnergyMask)
	return out
}

func (e *Encoder) syncCELTEnergyMask() {
	if e.celtEncoder == nil {
		return
	}
	if len(e.celtEnergyMask) == 0 {
		e.celtEncoder.SetEnergyMask(nil)
		return
	}
	n := min(len(e.celtEnergyMask), celt.MaxBands*2)
	e.celtEncoder.SetEnergyMask(e.celtEnergyMask[:n])
}

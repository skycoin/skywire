// encoder.go implements the public Encoder API for Opus encoding.

package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/types"
)

// Application hints the encoder for optimization.
type Application int

const (
	// ApplicationVoIP optimizes for speech transmission with low latency.
	// Prefers SILK mode for speech frequencies.
	ApplicationVoIP Application = iota

	// ApplicationAudio optimizes for music and high-quality audio.
	// Prefers CELT/Hybrid mode for full-bandwidth audio.
	ApplicationAudio

	// ApplicationLowDelay minimizes algorithmic delay.
	// Uses CELT mode exclusively with small frame sizes.
	ApplicationLowDelay

	// ApplicationRestrictedSilk forces SILK-only encoding.
	// This matches libopus OPUS_APPLICATION_RESTRICTED_SILK init-time behavior.
	ApplicationRestrictedSilk

	// ApplicationRestrictedCelt forces CELT-only encoding with low-delay semantics.
	// This matches libopus OPUS_APPLICATION_RESTRICTED_CELT init-time behavior.
	ApplicationRestrictedCelt
)

// Signal represents a hint about the input signal type.
// This helps the encoder optimize for speech or music content.
type Signal = types.Signal

const (
	// SignalAuto lets the encoder detect the signal type automatically.
	SignalAuto = types.SignalAuto
	// SignalVoice hints that the input is speech, biasing toward SILK mode.
	SignalVoice = types.SignalVoice
	// SignalMusic hints that the input is music, biasing toward CELT mode.
	SignalMusic = types.SignalMusic
)

// BitrateMode controls how the encoder sizes packets.
type BitrateMode = encoder.BitrateMode

const (
	// BitrateModeVBR enables unconstrained variable bitrate mode.
	BitrateModeVBR = encoder.ModeVBR
	// BitrateModeCVBR enables constrained variable bitrate mode.
	BitrateModeCVBR = encoder.ModeCVBR
	// BitrateModeCBR enables constant bitrate mode.
	BitrateModeCBR = encoder.ModeCBR
)

// EncoderMode controls the encoder's forced coding mode.
type EncoderMode = encoder.Mode

const (
	// EncoderModeAuto lets the encoder choose the coding mode.
	EncoderModeAuto = encoder.ModeAuto
	// EncoderModeSILK forces SILK-only encoding where the application permits it.
	EncoderModeSILK = encoder.ModeSILK
	// EncoderModeHybrid forces hybrid SILK+CELT encoding where valid.
	EncoderModeHybrid = encoder.ModeHybrid
	// EncoderModeCELT forces CELT-only encoding.
	EncoderModeCELT = encoder.ModeCELT
)

const (
	// BitrateAuto lets the encoder pick the bitrate from the sample rate,
	// channel count, and application (libopus OPUS_AUTO). It is the default.
	BitrateAuto = encoder.BitrateAuto
	// BitrateMax tells the encoder to use as many bits as the output buffer
	// allows for each frame (libopus OPUS_BITRATE_MAX).
	BitrateMax = encoder.BitrateMax
)

// In-band FEC modes for SetInBandFEC. These mirror the libopus
// OPUS_SET_INBAND_FEC values.
const (
	// InBandFECDisabled turns in-band forward error correction off (value 0).
	InBandFECDisabled = encoder.InBandFECDisabled
	// InBandFECEnabled enables in-band FEC for all signal types (value 1).
	InBandFECEnabled = encoder.InBandFECEnabled
	// InBandFECMusicSafe enables in-band FEC only for speech-like content,
	// leaving music frames untouched (value 2).
	InBandFECMusicSafe = encoder.InBandFECMusicSafe
)

// EncoderConfig configures an Encoder instance.
type EncoderConfig struct {
	// SampleRate must be one of: 8000, 12000, 16000, 24000, 48000.
	SampleRate int
	// Channels must be 1 (mono) or 2 (stereo).
	Channels int
	// Application hints the encoder for optimization.
	Application Application
}

// Encoder encodes PCM audio samples into Opus packets.
//
// An Encoder instance maintains internal state and is NOT safe for concurrent use.
// Each goroutine should create its own Encoder instance.
//
// The encoder supports three modes:
//   - SILK: optimized for speech at lower bitrates
//   - CELT: optimized for music and high-quality audio
//   - Hybrid: combines SILK and CELT for wideband speech
//
// The mode is automatically selected based on the Application hint and bandwidth settings.
//
// Zero-allocation design: All scratch buffers are pre-allocated at construction time.
// The Encode and EncodeInt16 methods perform zero heap allocations in the hot path
// when called with properly sized caller-provided buffers.
type Encoder struct {
	enc                 *encoder.Encoder
	sampleRate          int32
	channels            int32
	frameSize           int32
	expertFrameDuration ExpertFrameDuration
	application         Application
	modeSet             bool

	// Scratch buffers for zero-allocation encoding
	scratchPCM32 []float32 // int16 to float32 conversion buffer
	dnnBlob      *dnnblob.Blob
	encoderHD96kFields
}

// NewEncoder creates a new Opus encoder.
//
// Returns an error if the config is invalid.
func NewEncoder(cfg EncoderConfig) (*Encoder, error) {
	if !validSampleRate(cfg.SampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if cfg.Channels < 1 || cfg.Channels > 2 {
		return nil, ErrInvalidChannels
	}
	if !validApplication(cfg.Application) {
		return nil, ErrInvalidApplication
	}

	// Under gopus_qext, 96 kHz requests route through the 48 kHz internal
	// pipeline with 2:1 decimation at the input boundary.
	// C ref: opus_encoder.c opus_encoder_init() ENABLE_QEXT gate (Fs != 96000).
	internalRate := cfg.SampleRate
	if cfg.SampleRate == 96000 {
		internalRate = 48000
	}

	// Max frame size is 5760 samples (120ms at 48kHz) per channel.
	// At 96 kHz API, scratchPCM32 must hold 2*5760 samples.
	maxSamples := 5760 * cfg.Channels
	if cfg.SampleRate == 96000 {
		maxSamples = 2 * 5760 * cfg.Channels
	}

	enc := &Encoder{
		enc:                 encoder.NewEncoder(internalRate, cfg.Channels),
		sampleRate:          int32(cfg.SampleRate),
		channels:            int32(cfg.Channels),
		frameSize:           int32(internalRate / 50), // Default 20ms at the internal rate
		expertFrameDuration: ExpertFrameDurationArg,
		application:         cfg.Application,
		scratchPCM32:        make([]float32, maxSamples),
	}

	// Apply application hint
	if err := enc.applyApplication(cfg.Application); err != nil {
		return nil, err
	}

	if cfg.SampleRate == 96000 {
		init96kEncoder(enc)
	}

	return enc, nil
}

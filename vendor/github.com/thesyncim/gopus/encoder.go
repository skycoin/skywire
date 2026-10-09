package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/types"
)

// Application selects the encoder's operating profile. The standard profiles tune
// encoding for speech, general audio, or low delay; the restricted profiles force
// SILK-only or CELT-only encoding and can be selected only at construction.
type Application int

const (
	// ApplicationVoIP tunes the encoder for interactive speech.
	ApplicationVoIP Application = iota

	// ApplicationAudio tunes the encoder for general audio.
	ApplicationAudio

	// ApplicationLowDelay minimizes algorithmic delay.
	ApplicationLowDelay

	// ApplicationRestrictedSilk forces SILK-only encoding.
	// This matches libopus OPUS_APPLICATION_RESTRICTED_SILK init-time behavior.
	ApplicationRestrictedSilk

	// ApplicationRestrictedCelt forces CELT-only encoding with low-delay semantics.
	// This matches libopus OPUS_APPLICATION_RESTRICTED_CELT init-time behavior.
	ApplicationRestrictedCelt
)

// Signal is a hint about the input signal type.
type Signal = types.Signal

const (
	// SignalAuto lets the encoder detect the signal type automatically.
	SignalAuto = types.SignalAuto
	// SignalVoice hints that the input is speech, biasing toward SILK mode.
	SignalVoice = types.SignalVoice
	// SignalMusic hints that the input is music, biasing toward CELT mode.
	SignalMusic = types.SignalMusic
)

// BitrateMode selects variable, constrained-variable, or constant bitrate.
type BitrateMode = encoder.BitrateMode

const (
	// BitrateModeVBR enables unconstrained variable bitrate mode.
	BitrateModeVBR = encoder.ModeVBR
	// BitrateModeCVBR enables constrained variable bitrate mode.
	BitrateModeCVBR = encoder.ModeCVBR
	// BitrateModeCBR enables constant bitrate mode.
	BitrateModeCBR = encoder.ModeCBR
)

// EncoderMode selects automatic coding-mode selection or forces a coding mode.
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
	// BitrateAuto asks the encoder to choose a bitrate from the stream format and
	// application (libopus OPUS_AUTO).
	BitrateAuto = encoder.BitrateAuto
	// BitrateMax tells the encoder to use as many bits as the output buffer
	// allows for each frame (libopus OPUS_BITRATE_MAX).
	BitrateMax = encoder.BitrateMax
)

// In-band FEC modes accepted by SetInBandFEC.
const (
	// InBandFECDisabled turns in-band forward error correction off (value 0).
	InBandFECDisabled = encoder.InBandFECDisabled
	// InBandFECEnabled enables in-band FEC for all signal types (value 1).
	InBandFECEnabled = encoder.InBandFECEnabled
	// InBandFECMusicSafe enables in-band FEC only for speech-like content,
	// leaving music frames untouched (value 2).
	InBandFECMusicSafe = encoder.InBandFECMusicSafe
)

// EncoderConfig describes the input format and application profile for an
// Encoder.
type EncoderConfig struct {
	// SampleRate must be 8000, 12000, 16000, 24000, or 48000 Hz.
	// Builds with gopus_qext also accept 96000 Hz.
	SampleRate int
	// Channels must be 1 (mono) or 2 (stereo).
	Channels int
	// Application selects the operating profile. Its zero value, ApplicationVoIP,
	// is used when the field is omitted. Restricted profiles force one coding mode
	// and can be selected only when NewEncoder creates the encoder.
	Application Application
}

// Encoder encodes one interleaved PCM stream into Opus packets. Construct it
// with NewEncoder; the zero value is not ready for use. Encoder retains codec
// state across calls and is not safe for concurrent use, so use one Encoder per
// stream. Encode, EncodeInt16, and EncodeInt24 write packets into caller-provided
// buffers; the Slice methods return owned packet slices.
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

// NewEncoder returns an initialized Encoder for cfg. Its configured frame size
// starts at 20 ms and its target bitrate starts at 64,000 bits per second. A
// zero-valued Application selects ApplicationVoIP. It returns
// ErrInvalidSampleRate, ErrInvalidChannels, or ErrInvalidApplication when cfg
// contains an unsupported value.
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

	// The public 96 kHz API stores its default frame size in 48 kHz-equivalent
	// samples; the core encoder retains native Fs for SILK and CELT state.
	// C ref: opus_encoder.c opus_encoder_init() ENABLE_QEXT rate selection.
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
		enc:                 encoder.NewEncoder(cfg.SampleRate, cfg.Channels),
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

// multistream.go implements the public Multistream API for Opus surround sound encoding and decoding.

package gopus

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/multistream"
)

func multistreamExplicitChannelsError(kind string, channels int) error {
	return fmt.Errorf("%w: %s supports 1-255 channels (got %d)", ErrInvalidChannels, kind, channels)
}

func multistreamDefaultChannelsError(kind, explicitCtor string, channels int) error {
	if channels > 8 {
		return fmt.Errorf("%w: %s supports 1-8 channels (got %d); use %s with an explicit mapping for >8 channels", ErrInvalidChannels, kind, channels, explicitCtor)
	}
	return fmt.Errorf("%w: %s supports 1-8 channels (got %d)", ErrInvalidChannels, kind, channels)
}

func multistreamStreamBudgetError(kind string, streams, coupledStreams int) error {
	total := streams + coupledStreams
	return fmt.Errorf("%w: %s requires streams + coupledStreams <= 255 (got %d + %d = %d)", ErrInvalidStreams, kind, streams, coupledStreams, total)
}

func multistreamMappingLengthError(channels, got int) error {
	return fmt.Errorf("%w: expected %d mapping entries for %d channels, got %d", ErrInvalidMapping, channels, channels, got)
}

// MultistreamEncoder encodes multi-channel PCM audio into Opus multistream packets.
//
// A MultistreamEncoder instance maintains internal state and is NOT safe for concurrent use.
// Each goroutine should create its own MultistreamEncoder instance.
//
// Multistream encoding is used for surround sound configurations (5.1, 7.1, etc.)
// where multiple coupled (stereo) and uncoupled (mono) streams are combined.
//
// Reference: RFC 6716 Appendix B, RFC 7845 Section 5.1.1
type MultistreamEncoder struct {
	enc                 *multistream.Encoder
	sampleRate          int32
	channels            int32
	frameSize           int32
	expertFrameDuration ExpertFrameDuration
	application         Application
	encodedOnce         bool
	modeSet             bool
	scratchPCM32        []float32
	dnnBlob             *dnnblob.Blob
}

// NewMultistreamEncoder creates a new multistream encoder with explicit configuration.
//
// Parameters:
//   - sampleRate: input sample rate (8000, 12000, 16000, 24000, or 48000 Hz)
//   - channels: total input channels (1-255)
//   - streams: total elementary streams (N, 1-255)
//   - coupledStreams: number of coupled stereo streams (M, 0 to streams)
//     with streams + coupledStreams <= 255
//   - mapping: channel mapping table (length must equal channels)
//   - application: application hint for encoder optimization
//
// Returns ErrInvalidChannels when channels is outside the explicit multistream
// range [1, 255].
//
// The mapping table determines how input audio is routed to stream encoders:
//   - Values 0 to 2*M-1: to coupled streams (even=left, odd=right of stereo pair)
//   - Values 2*M to N+M-1: to uncoupled (mono) streams
//   - Value 255: silent channel (input ignored)
//
// Example for 5.1 surround (6 channels, 4 streams, 2 coupled):
//
//	mapping = [0, 4, 1, 2, 3, 5]
//	  Input 0 (FL): mapping[0]=0 -> coupled stream 0, left
//	  Input 1 (C):  mapping[1]=4 -> uncoupled stream 2 (2*2+0)
//	  Input 2 (FR): mapping[2]=1 -> coupled stream 0, right
//	  Input 3 (RL): mapping[3]=2 -> coupled stream 1, left
//	  Input 4 (RR): mapping[4]=3 -> coupled stream 1, right
//	  Input 5 (LFE): mapping[5]=5 -> uncoupled stream 3 (2*2+1)
func NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams int, mapping []byte, application Application) (*MultistreamEncoder, error) {
	if !validSampleRate(sampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if channels < 1 || channels > 255 {
		return nil, multistreamExplicitChannelsError("multistream encoder", channels)
	}
	if streams < 1 || streams > 255 {
		return nil, ErrInvalidStreams
	}
	if coupledStreams < 0 || coupledStreams > streams {
		return nil, ErrInvalidCoupledStreams
	}
	if streams+coupledStreams > 255 {
		return nil, multistreamStreamBudgetError("multistream encoder", streams, coupledStreams)
	}
	if len(mapping) != channels {
		return nil, multistreamMappingLengthError(channels, len(mapping))
	}
	if !validApplication(application) {
		return nil, ErrInvalidApplication
	}

	enc, err := multistream.NewEncoder(sampleRate, channels, streams, coupledStreams, mapping)
	if err != nil {
		return nil, err
	}

	mse := &MultistreamEncoder{
		enc:                 enc,
		sampleRate:          int32(sampleRate),
		channels:            int32(channels),
		frameSize:           int32(sampleRate / 50), // Default 20ms at the native rate
		expertFrameDuration: ExpertFrameDurationArg,
		application:         application,
		scratchPCM32:        make([]float32, 5760*channels),
	}

	// Apply application hint
	if err := mse.applyApplication(application); err != nil {
		return nil, err
	}

	return mse, nil
}

// NewMultistreamEncoderDefault creates a multistream encoder with default Vorbis-style mapping
// for standard channel configurations (1-8 channels).
//
// This is a convenience function that calls the internal DefaultMapping() to get the appropriate
// streams, coupledStreams, and mapping for the given channel count.
// Returns ErrInvalidChannels when channels is outside the default-mapping range [1, 8].
// Use NewMultistreamEncoder with an explicit mapping for layouts above 8 channels.
//
// Supported channel counts:
//   - 1: mono (1 stream, 0 coupled)
//   - 2: stereo (1 stream, 1 coupled)
//   - 3: 3.0 (2 streams, 1 coupled)
//   - 4: quad (2 streams, 2 coupled)
//   - 5: 5.0 (3 streams, 2 coupled)
//   - 6: 5.1 surround (4 streams, 2 coupled)
//   - 7: 6.1 surround (4 streams, 3 coupled)
//   - 8: 7.1 surround (5 streams, 3 coupled)
func NewMultistreamEncoderDefault(sampleRate, channels int, application Application) (*MultistreamEncoder, error) {
	if !validSampleRate(sampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if channels < 1 || channels > 8 {
		return nil, multistreamDefaultChannelsError("default multistream encoder", "NewMultistreamEncoder", channels)
	}
	if !validApplication(application) {
		return nil, ErrInvalidApplication
	}

	enc, err := multistream.NewEncoderDefault(sampleRate, channels)
	if err != nil {
		return nil, err
	}

	mse := &MultistreamEncoder{
		enc:                 enc,
		sampleRate:          int32(sampleRate),
		channels:            int32(channels),
		frameSize:           int32(sampleRate / 50), // Default 20ms at the native rate
		expertFrameDuration: ExpertFrameDurationArg,
		application:         application,
		scratchPCM32:        make([]float32, 5760*channels),
	}

	// Apply application hints
	if err := mse.applyApplication(application); err != nil {
		return nil, err
	}

	return mse, nil
}

// MultistreamDecoder decodes Opus multistream packets into multi-channel PCM audio.
//
// A MultistreamDecoder instance maintains internal state and is NOT safe for concurrent use.
// Each goroutine should create its own MultistreamDecoder instance.
//
// Multistream decoding is used for surround sound configurations (5.1, 7.1, etc.)
// where multiple coupled (stereo) and uncoupled (mono) streams are combined.
//
// Reference: RFC 6716 Appendix B, RFC 7845 Section 5.1.1
type MultistreamDecoder struct {
	dec              *multistream.Decoder
	sampleRate       int32
	channels         int32
	lastFrameSize    int32
	ignoreExtensions bool
	dnnBlob          *dnnblob.Blob
	softClipMem      []float32
}

// NewMultistreamDecoder creates a new multistream decoder with explicit configuration.
//
// Parameters:
//   - sampleRate: output sample rate (8000, 12000, 16000, 24000, or 48000 Hz)
//   - channels: total output channels (1-255)
//   - streams: total elementary streams (N, 1-255)
//   - coupledStreams: number of coupled stereo streams (M, 0 to streams)
//     with streams + coupledStreams <= 255
//   - mapping: channel mapping table (length must equal channels)
//
// Returns ErrInvalidChannels when channels is outside the explicit multistream
// range [1, 255].
//
// The mapping table determines how decoded audio is routed to output channels:
//   - Values 0 to 2*M-1: from coupled streams (even=left, odd=right of stereo pair)
//   - Values 2*M to N+M-1: from uncoupled (mono) streams
//   - Value 255: silent channel (output zeros)
func NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*MultistreamDecoder, error) {
	if !validSampleRate(sampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if channels < 1 || channels > 255 {
		return nil, multistreamExplicitChannelsError("multistream decoder", channels)
	}
	if streams < 1 || streams > 255 {
		return nil, ErrInvalidStreams
	}
	if coupledStreams < 0 || coupledStreams > streams {
		return nil, ErrInvalidCoupledStreams
	}
	if streams+coupledStreams > 255 {
		return nil, multistreamStreamBudgetError("multistream decoder", streams, coupledStreams)
	}
	if len(mapping) != channels {
		return nil, multistreamMappingLengthError(channels, len(mapping))
	}

	dec, err := multistream.NewDecoder(sampleRate, channels, streams, coupledStreams, mapping)
	if err != nil {
		return nil, err
	}

	return &MultistreamDecoder{
		dec:           dec,
		sampleRate:    int32(sampleRate),
		channels:      int32(channels),
		lastFrameSize: int32(sampleRate / 50),
		softClipMem:   make([]float32, channels),
	}, nil
}

// NewMultistreamDecoderDefault creates a multistream decoder with default Vorbis-style mapping
// for standard channel configurations (1-8 channels).
//
// This is a convenience function that calls the internal DefaultMapping() to get the appropriate
// streams, coupledStreams, and mapping for the given channel count.
// Returns ErrInvalidChannels when channels is outside the default-mapping range [1, 8].
// Use NewMultistreamDecoder with an explicit mapping for layouts above 8 channels.
//
// Supported channel counts:
//   - 1: mono (1 stream, 0 coupled)
//   - 2: stereo (1 stream, 1 coupled)
//   - 3: 3.0 (2 streams, 1 coupled)
//   - 4: quad (2 streams, 2 coupled)
//   - 5: 5.0 (3 streams, 2 coupled)
//   - 6: 5.1 surround (4 streams, 2 coupled)
//   - 7: 6.1 surround (4 streams, 3 coupled)
//   - 8: 7.1 surround (5 streams, 3 coupled)
func NewMultistreamDecoderDefault(sampleRate, channels int) (*MultistreamDecoder, error) {
	if !validSampleRate(sampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if channels < 1 || channels > 8 {
		return nil, multistreamDefaultChannelsError("default multistream decoder", "NewMultistreamDecoder", channels)
	}

	dec, err := multistream.NewDecoderDefault(sampleRate, channels)
	if err != nil {
		return nil, err
	}

	return &MultistreamDecoder{
		dec:           dec,
		sampleRate:    int32(sampleRate),
		channels:      int32(channels),
		lastFrameSize: int32(sampleRate / 50),
		softClipMem:   make([]float32, channels),
	}, nil
}

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

// MultistreamEncoder encodes interleaved multichannel PCM into Opus
// multistream packets. It retains stream state and is not safe for concurrent
// use; use one encoder per stream.
type MultistreamEncoder struct {
	enc                 *multistream.Encoder
	sampleRate          int32
	channels            int32
	frameSize           int32
	expertFrameDuration ExpertFrameDuration
	application         Application
	modeSet             bool
	scratchPCM32        []float32
	dnnBlob             *dnnblob.Blob
}

// NewMultistreamEncoder returns an encoder for an explicit channel mapping.
// sampleRate is in hertz and must be 8, 12, 16, 24, or 48 kHz; 96 kHz is
// available in builds tagged gopus_qext. channels is the number of input
// channels (1–255), streams is the number of elementary streams (1–255), and
// coupledStreams is the number of initial stereo streams (0–streams). The sum
// streams+coupledStreams must not exceed 255.
//
// mapping must contain one entry per input channel. Values 0 through
// 2*coupledStreams-1 select left or right channels of coupled streams (even is
// left, odd is right); subsequent values select mono streams. The value 255
// omits an input channel. Every stream must receive input, and both channels of
// every coupled stream must be mapped. The constructor copies mapping.
//
// The constructor returns an error for an unsupported rate, invalid count,
// mapping or layout, or invalid application.
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
	maxSamples := sampleRate * 120 / 1000 * channels

	mse := &MultistreamEncoder{
		enc:                 enc,
		sampleRate:          int32(sampleRate),
		channels:            int32(channels),
		frameSize:           int32(sampleRate / 50), // Default 20ms at the native rate
		expertFrameDuration: ExpertFrameDurationArg,
		application:         application,
		scratchPCM32:        make([]float32, maxSamples),
	}

	// Apply application hint
	if err := mse.applyApplication(application); err != nil {
		return nil, err
	}

	return mse, nil
}

// NewMultistreamEncoderDefault returns an encoder with the Vorbis mapping for
// 1–8 input channels. Use NewMultistreamEncoder for other layouts. It returns
// an error for an unsupported sample rate, channel count, or application.
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
	maxSamples := sampleRate * 120 / 1000 * channels

	mse := &MultistreamEncoder{
		enc:                 enc,
		sampleRate:          int32(sampleRate),
		channels:            int32(channels),
		frameSize:           int32(sampleRate / 50), // Default 20ms at the native rate
		expertFrameDuration: ExpertFrameDurationArg,
		application:         application,
		scratchPCM32:        make([]float32, maxSamples),
	}

	// Apply application hints
	if err := mse.applyApplication(application); err != nil {
		return nil, err
	}

	return mse, nil
}

// MultistreamDecoder decodes Opus multistream packets into interleaved PCM. It
// retains stream state and is not safe for concurrent use; use one decoder per
// stream.
type MultistreamDecoder struct {
	dec              *multistream.Decoder
	sampleRate       int32
	channels         int32
	lastFrameSize    int32
	ignoreExtensions bool
	dnnBlob          *dnnblob.Blob
	softClipMem      []float32
	decodeScratch    []float32
}

// NewMultistreamDecoder returns a decoder for an explicit channel mapping.
// sampleRate is in hertz and must be 8, 12, 16, 24, or 48 kHz; 96 kHz is
// available in builds tagged gopus_qext. channels is the number of output
// channels (1–255), streams is the number of elementary streams (1–255), and
// coupledStreams is the number of initial stereo streams (0–streams). The sum
// streams+coupledStreams must not exceed 255.
//
// mapping must contain one entry per output channel. Values 0 through
// 2*coupledStreams-1 select left or right channels of coupled streams (even is
// left, odd is right); subsequent values select mono streams. Repeated values
// duplicate a decoded channel, and 255 produces silence. The constructor copies
// mapping.
//
// The constructor returns an error for an unsupported rate, invalid count, or
// invalid mapping.
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

// NewMultistreamDecoderDefault returns a decoder with the Vorbis mapping for
// 1–8 output channels. Use NewMultistreamDecoder for other layouts. It returns
// an error for an unsupported sample rate or channel count.
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

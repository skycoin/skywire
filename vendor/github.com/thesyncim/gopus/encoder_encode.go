package gopus

import (
	"errors"

	"github.com/thesyncim/gopus/internal/encoder"
)

func translateEncoderError(err error) error {
	if errors.Is(err, encoder.ErrBufferTooSmall) {
		return ErrBufferTooSmall
	}
	return err
}

// Encode encodes one interleaved float32 PCM frame. pcm must contain exactly
// FrameSize()*Channels() samples. A fixed ExpertFrameDuration can select a
// shorter prefix, but pcm must still contain the configured frame. len(data) is
// both the packet byte budget and destination; Encode copies the packet there
// and returns the number of bytes written. It returns ErrInvalidFrameSize for
// invalid frame geometry and ErrBufferTooSmall when the output budget cannot
// hold the packet. During DTX, silence can produce a one-byte TOC-only packet.
func (e *Encoder) Encode(pcm []float32, data []byte) (int, error) {
	if e.is96kHz() {
		return e.encode96k(pcm, data, encoder.EncodeInputFloat32)
	}
	frameSizeArg := int(e.frameSize)
	channels := int(e.channels)
	expected := frameSizeArg * channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}
	frameSize, err := selectExpertFrameSize(frameSizeArg, e.expertFrameDuration, e.application, e.internalSampleRate())
	e.enc.BeginEncodeCall(encoder.EncodeInputFloat32, frameSize)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, ErrBufferTooSmall
	}
	inputSamples := frameSize * channels

	packet, err := e.enc.EncodeFloat32WithAnalysisMaxBytes(pcm[:inputSamples], frameSize, pcm, len(data))
	if err != nil {
		return 0, translateEncoderError(err)
	}

	return copyEncodedPacket(packet, data)
}

// encode96k handles Encode for a 96 kHz API-rate Encoder. The selected QEXT
// build routes native-rate PCM through the shared mode and history driver.
func (e *Encoder) encode96k(pcm []float32, data []byte, input encoder.EncodeInputFormat) (int, error) {
	if n, handled, err := e.tryEncodeNative96k(pcm, data, input); handled {
		return n, translateEncoderError(err)
	}
	return 0, ErrInvalidSampleRate
}

// EncodeInt16 encodes one interleaved signed 16-bit PCM frame. pcm must contain
// exactly FrameSize()*Channels() samples. Each input sample is scaled by
// 1/32768. len(data) is both the packet byte budget and destination. It returns
// the number of bytes written, ErrInvalidFrameSize for an incorrect input
// length or selected frame, or ErrBufferTooSmall when the packet cannot fit.
func (e *Encoder) EncodeInt16(pcm []int16, data []byte) (int, error) {
	expected := e.apiFrameSize() * int(e.channels)
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}

	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v) / 32768.0
	}
	return e.encodeInt16Packet(pcm32, data)
}

// encodeInt16Packet uses opus_encode_native's short-input policy, including
// the per-call 16-bit LSB-depth cap and short-input analysis callback.
func (e *Encoder) encodeInt16Packet(pcm32 []float32, data []byte) (int, error) {
	if e.is96kHz() {
		// opus_encode_native caps the configured LSB depth at 16 bits for the
		// short API before selecting the native 96 kHz CELT path.
		configuredDepth := e.enc.LSBDepth()
		if configuredDepth > 16 {
			e.enc.SetLSBDepth(16)
		}
		defer e.enc.SetLSBDepth(configuredDepth)
		return e.encode96k(pcm32, data, encoder.EncodeInputInt16)
	}
	frameSize, err := selectExpertFrameSize(int(e.frameSize), e.expertFrameDuration, e.application, e.internalSampleRate())
	e.enc.BeginEncodeCall(encoder.EncodeInputInt16, frameSize)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, ErrBufferTooSmall
	}
	packet, err := e.enc.EncodeShortMixedWithAnalysisMaxBytes(pcm32[:frameSize*int(e.channels)], frameSize, pcm32, len(data))
	if err != nil {
		return 0, translateEncoderError(err)
	}
	return copyEncodedPacket(packet, data)
}

// EncodeInt24 encodes one interleaved signed 24-bit PCM frame. Each int32 in
// pcm must be right-justified in [-8388608, 8388607], and pcm must contain
// exactly FrameSize()*Channels() samples. len(data) is both the packet byte
// budget and destination. It returns the number of bytes written,
// ErrInvalidFrameSize for invalid input geometry, or ErrBufferTooSmall when
// the packet cannot fit.
func (e *Encoder) EncodeInt24(pcm []int32, data []byte) (int, error) {
	channels := int(e.channels)
	expected := e.apiFrameSize() * channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}
	if e.is96kHz() {
		pcm32 := e.convertInt24ToFloat32(pcm)
		return e.encode96k(pcm32, data, encoder.EncodeInputInt24)
	}

	frameSizeArg := int(e.frameSize)
	frameSize, err := selectExpertFrameSize(frameSizeArg, e.expertFrameDuration, e.application, e.internalSampleRate())
	e.enc.BeginEncodeCall(encoder.EncodeInputInt24, frameSize)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, ErrBufferTooSmall
	}
	pcm32 := e.convertInt24ToFloat32(pcm)
	inputSamples := frameSize * channels

	packet, err := e.enc.EncodeFloat32WithAnalysisMaxBytes(pcm32[:inputSamples], frameSize, pcm32, len(data))
	if err != nil {
		return 0, translateEncoderError(err)
	}

	return copyEncodedPacket(packet, data)
}

func (e *Encoder) convertInt24ToFloat32(pcm []int32) []float32 {
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v) / 8388608.0
	}
	return pcm32
}

// EncodeFloat32 encodes one interleaved float32 PCM frame and returns a newly
// allocated packet slice that remains valid across later Encode calls. pcm must
// contain exactly FrameSize()*Channels() samples.
func (e *Encoder) EncodeFloat32(pcm []float32) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream, func(data []byte) (int, error) {
		return e.Encode(pcm, data)
	})
}

// EncodeInt16Slice encodes one interleaved signed 16-bit PCM frame and returns
// a newly allocated packet slice that remains valid across later Encode calls.
// pcm must contain exactly FrameSize()*Channels() samples; input is scaled by
// 1/32768.
func (e *Encoder) EncodeInt16Slice(pcm []int16) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream, func(data []byte) (int, error) {
		return e.EncodeInt16(pcm, data)
	})
}

// EncodeInt24Slice encodes one interleaved signed 24-bit PCM frame and returns
// a newly allocated packet slice that remains valid across later Encode calls.
// Each int32 sample must be right-justified in [-8388608, 8388607], and pcm
// must contain exactly FrameSize()*Channels() samples.
func (e *Encoder) EncodeInt24Slice(pcm []int32) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream, func(data []byte) (int, error) {
		return e.EncodeInt24(pcm, data)
	})
}

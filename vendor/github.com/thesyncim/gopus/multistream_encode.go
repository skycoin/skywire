package gopus

// Encode encodes float32 PCM samples into an Opus multistream packet.
//
// pcm: Input samples (interleaved). Length must be frameSize * channels.
// data: Output buffer for the encoded packet. Recommended size is 4000 bytes per stream.
//
// Returns the number of bytes written to data, or an error.
// Returns 0 bytes written if DTX suppresses all frames (silence detected in all streams).
func (e *MultistreamEncoder) Encode(pcm []float32, data []byte) (int, error) {
	frameSizeArg := int(e.frameSize)
	channels := int(e.channels)
	expected := frameSizeArg * channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}
	frameSize, err := selectExpertFrameSize(frameSizeArg, e.expertFrameDuration, e.application, int(e.sampleRate))
	if err != nil {
		return 0, err
	}
	inputSamples := frameSize * channels

	// libopus threads the caller buffer size (max_data_bytes) into the per-stream
	// curr_max budgeting (opus_multistream_encoder.c opus_multistream_encode_native()),
	// so pass the caller output buffer length rather than a fixed per-stream cap.
	packet, err := e.enc.EncodeFloat32WithAnalysisMaxBytes(pcm[:inputSamples], frameSize, pcm, len(data))
	if err != nil {
		return 0, err
	}
	e.encodedOnce = true

	return copyEncodedPacket(packet, data)
}

// EncodeInt16 encodes int16 PCM samples into an Opus multistream packet.
//
// pcm: Input samples (interleaved). Length must be frameSize * channels.
// data: Output buffer for the encoded packet.
//
// Returns the number of bytes written to data, or an error.
func (e *MultistreamEncoder) EncodeInt16(pcm []int16, data []byte) (int, error) {
	expected := int(e.frameSize) * int(e.channels)
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}

	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v) / 32768.0
	}
	return e.Encode(pcm32, data)
}

// EncodeInt24 encodes 24-bit PCM samples stored in int32 values into an Opus multistream packet.
//
// pcm: Input samples (interleaved). Length must be frameSize * channels.
// data: Output buffer for the encoded packet.
//
// Returns the number of bytes written to data, or an error.
//
// The input values are interpreted as right-justified signed 24-bit PCM
// carried in int32 containers with numeric range [-8388608, 8388607].
// Left-shifted 24-in-32 input will be mis-scaled.
func (e *MultistreamEncoder) EncodeInt24(pcm []int32, data []byte) (int, error) {
	expected := int(e.frameSize) * int(e.channels)
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v) / 8388608.0
	}
	return e.Encode(pcm32, data)
}

// EncodeFloat32 encodes float32 PCM samples and returns a new byte slice.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use Encode with a pre-allocated buffer.
//
// pcm: Input samples (interleaved).
//
// Returns the encoded packet or an error.
func (e *MultistreamEncoder) EncodeFloat32(pcm []float32) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream*e.enc.Streams(), func(data []byte) (int, error) {
		return e.Encode(pcm, data)
	})
}

// EncodeInt16Slice encodes int16 PCM samples and returns a new byte slice.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use EncodeInt16 with a pre-allocated buffer.
//
// pcm: Input samples (interleaved).
//
// Returns the encoded packet or an error.
func (e *MultistreamEncoder) EncodeInt16Slice(pcm []int16) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream*e.enc.Streams(), func(data []byte) (int, error) {
		return e.EncodeInt16(pcm, data)
	})
}

// EncodeInt24Slice encodes 24-bit PCM samples stored in int32 values and returns a new byte slice.
//
// This is a convenience method that allocates the output buffer.
func (e *MultistreamEncoder) EncodeInt24Slice(pcm []int32) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream*e.enc.Streams(), func(data []byte) (int, error) {
		return e.EncodeInt24(pcm, data)
	})
}

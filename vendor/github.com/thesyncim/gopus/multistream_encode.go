package gopus

// Encode encodes interleaved float32 PCM. pcm must contain exactly
// FrameSize()*Channels() samples; ExpertFrameDuration may select a shorter
// coded frame from that input. data is the packet buffer, and its length is the
// total byte budget shared by all elementary streams. A 4,000-byte-per-stream
// buffer is sufficient for a maximum packet. Encode returns the number of bytes
// written or an error; a buffer that is too small returns ErrBufferTooSmall.
// The steady-state path is allocation-free.
func (e *MultistreamEncoder) Encode(pcm []float32, data []byte) (int, error) {
	frameSize, err := e.codedFrameSize(len(pcm))
	if err != nil {
		return 0, err
	}
	n, err := e.enc.EncodeWithAnalysis(pcm[:frameSize*int(e.channels)], frameSize, pcm, data)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// codedFrameSize validates the caller frame length and returns the frame size
// frame_size_select() codes for it.
func (e *MultistreamEncoder) codedFrameSize(samples int) (int, error) {
	frameSizeArg := int(e.frameSize)
	if samples != frameSizeArg*int(e.channels) {
		return 0, ErrInvalidFrameSize
	}
	return selectExpertFrameSize(frameSizeArg, e.expertFrameDuration, e.application, int(e.sampleRate))
}

// EncodeInt16 encodes interleaved signed 16-bit PCM. pcm must contain exactly
// FrameSize()*Channels() samples; ExpertFrameDuration may select a shorter
// coded frame from that input. data is the packet buffer, and its length is the
// total byte budget shared by all elementary streams. It returns the number of
// bytes written or an error; a buffer that is too small returns
// ErrBufferTooSmall. Samples are scaled by 1/32768 and coded with a 16-bit LSB
// depth, matching opus_multistream_encode().
func (e *MultistreamEncoder) EncodeInt16(pcm []int16, data []byte) (int, error) {
	frameSize, err := e.codedFrameSize(len(pcm))
	if err != nil {
		return 0, err
	}
	n, err := e.enc.EncodeInt16WithAnalysis(pcm[:frameSize*int(e.channels)], frameSize, pcm, data)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// EncodeInt24 encodes interleaved signed 24-bit PCM carried in int32 values.
// pcm must contain exactly FrameSize()*Channels() values; ExpertFrameDuration
// may select a shorter coded frame from that input. data is the packet buffer,
// and its length is the total byte budget shared by all elementary streams. It
// returns the number of bytes written or an error; a buffer that is too small
// returns ErrBufferTooSmall.
//
// Each input value must be right-justified in the range [-8388608, 8388607].
// Left-shifted 24-in-32 PCM is mis-scaled. Values are converted to float32 by
// dividing by 8388608 and coded at a 24-bit LSB depth.
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

// EncodeFloat32 encodes interleaved float32 PCM and returns a newly allocated
// packet slice. The input must contain exactly FrameSize()*Channels() samples;
// ExpertFrameDuration may select a shorter coded frame from that input.
// For allocation-free steady-state encoding, use Encode with a reusable packet
// buffer.
func (e *MultistreamEncoder) EncodeFloat32(pcm []float32) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream*e.enc.Streams(), func(data []byte) (int, error) {
		return e.Encode(pcm, data)
	})
}

// EncodeInt16Slice encodes interleaved signed 16-bit PCM and returns a newly
// allocated packet slice. The input must contain exactly FrameSize()*Channels()
// samples; ExpertFrameDuration may select a shorter coded frame. For
// allocation-free steady-state encoding, use EncodeInt16 with a reusable
// packet buffer.
func (e *MultistreamEncoder) EncodeInt16Slice(pcm []int16) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream*e.enc.Streams(), func(data []byte) (int, error) {
		return e.EncodeInt16(pcm, data)
	})
}

// EncodeInt24Slice encodes interleaved right-justified signed 24-bit PCM stored
// in int32 values and returns a newly allocated packet slice. The input must
// contain exactly FrameSize()*Channels() values; ExpertFrameDuration may select
// a shorter coded frame. For allocation-free steady-state encoding, use
// EncodeInt24 with a reusable packet buffer.
func (e *MultistreamEncoder) EncodeInt24Slice(pcm []int32) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream*e.enc.Streams(), func(data []byte) (int, error) {
		return e.EncodeInt24(pcm, data)
	})
}

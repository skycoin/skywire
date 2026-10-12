package gopus

import "github.com/thesyncim/gopus/multistream"

func (d *MultistreamDecoder) requestedOutputFrameSize(sampleCount int) (int, error) {
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	if channels <= 0 {
		return 0, ErrInvalidChannels
	}
	if sampleCount < channels {
		return 0, ErrBufferTooSmall
	}
	if sampleCount%channels != 0 {
		return 0, ErrInvalidFrameSize
	}
	frameSize := sampleCount / channels
	if frameSize <= 0 {
		return 0, ErrBufferTooSmall
	}
	maxPLCFrameSize := sampleRate / 25 * 3
	if maxPLCFrameSize <= 0 {
		return 0, ErrInvalidFrameSize
	}
	if frameSize > maxPLCFrameSize {
		return maxPLCFrameSize, nil
	}
	quantum := sampleRate / 400
	if quantum <= 0 || frameSize%quantum != 0 {
		return 0, ErrInvalidFrameSize
	}
	return frameSize, nil
}

func (d *MultistreamDecoder) decodeFrameSize(data []byte, sampleCount int) (int, error) {
	// A nil OR zero-length packet is packet loss: libopus opus_multistream_decode
	// sets do_plc=1 for len==0 (opus_multistream_decoder.c:213) and conceals the
	// requested output frame size, exactly as for a NULL packet.
	if len(data) == 0 {
		return d.requestedOutputFrameSize(sampleCount)
	}
	return d.dec.PacketDurationAtRate(data)
}

func (d *MultistreamDecoder) decodeScratchFor(n int) []float32 {
	if cap(d.decodeScratch) < n {
		d.decodeScratch = make([]float32, n)
	}
	return d.decodeScratch[:n]
}

func (d *MultistreamDecoder) nextPLCChunkSamples(remaining int) int {
	chunk := int(d.sampleRate) / 50
	if chunk <= 0 || remaining < chunk {
		return remaining
	}
	return chunk
}

func (d *MultistreamDecoder) decodePLCFloat32Into(pcm []float32, frameSize int) error {
	channels := int(d.channels)
	remaining := frameSize
	offset := 0
	for remaining > 0 {
		chunk := d.nextPLCChunkSamples(remaining)
		if chunk <= 0 {
			return ErrInvalidFrameSize
		}
		total := chunk * channels
		if offset+total > len(pcm) {
			return ErrBufferTooSmall
		}
		n, err := d.dec.DecodeIntoFloat32(nil, pcm[offset:offset+total], chunk)
		if err != nil {
			return err
		}
		if n != chunk {
			return ErrBufferTooSmall
		}
		offset += total
		remaining -= chunk
	}
	return nil
}

func (d *MultistreamDecoder) decodePLCInt16Into(pcm []int16, frameSize int) error {
	channels := int(d.channels)
	remaining := frameSize
	offset := 0
	for remaining > 0 {
		chunk := d.nextPLCChunkSamples(remaining)
		if chunk <= 0 {
			return ErrInvalidFrameSize
		}
		total := chunk * channels
		if offset+total > len(pcm) {
			return ErrBufferTooSmall
		}
		samples := d.decodeScratchFor(total)
		n, err := d.dec.DecodeIntoFloat32(nil, samples, chunk)
		if err != nil {
			return err
		}
		if n != chunk {
			return ErrBufferTooSmall
		}
		float32ToInt16NoSoftClipScalar(pcm[offset:offset+total], samples, chunk, channels)
		offset += total
		remaining -= chunk
	}
	return nil
}

func (d *MultistreamDecoder) decodePLCInt24Into(pcm []int32, frameSize int) error {
	channels := int(d.channels)
	remaining := frameSize
	offset := 0
	for remaining > 0 {
		chunk := d.nextPLCChunkSamples(remaining)
		if chunk <= 0 {
			return ErrInvalidFrameSize
		}
		total := chunk * channels
		if offset+total > len(pcm) {
			return ErrBufferTooSmall
		}
		samples := d.decodeScratchFor(total)
		n, err := d.dec.DecodeIntoFloat32(nil, samples, chunk)
		if err != nil {
			return err
		}
		if n != chunk {
			return ErrBufferTooSmall
		}
		float32ToInt24Slice(pcm[offset:offset+total], samples, chunk, channels)
		offset += total
		remaining -= chunk
	}
	return nil
}

// Decode decodes a packet into interleaved float32 PCM, or performs packet loss
// concealment (PLC) when data is nil or empty. It returns the number of samples
// per channel written. For a packet, pcm must have room for that packet's
// duration; a larger buffer is allowed, and its unused tail is left unchanged.
//
// For PLC, the per-channel request size is inferred from len(pcm)/Channels(),
// not from the previous packet. The buffer length must contain whole interleaved
// frames; requests above 120 ms are capped to 120 ms. A buffer too small for a
// packet or a PLC frame returns ErrBufferTooSmall. A malformed PLC frame length
// returns ErrInvalidFrameSize.
func (d *MultistreamDecoder) Decode(data []byte, pcm []float32) (int, error) {
	channels := int(d.channels)
	if len(data) != 0 {
		if len(pcm) < channels {
			return 0, ErrBufferTooSmall
		}
		n, err := d.dec.DecodeIntoFloat32(data, pcm, len(pcm)/channels)
		if err == multistream.ErrBufferTooSmall {
			return 0, ErrBufferTooSmall
		}
		if err != nil {
			return 0, err
		}
		// opus_decode_native clears soft-clip history after a successful
		// packet decoded without clipping. PLC and errors preserve it.
		clear(d.softClipMem)
		d.lastFrameSize = int32(n)
		return n, nil
	}
	frameSize, err := d.requestedOutputFrameSize(len(pcm))
	if err != nil {
		return 0, err
	}
	needed := frameSize * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}
	if handled, err := d.fixedDecodePLCFloat32(pcm[:needed], frameSize); err != nil {
		return 0, err
	} else if handled {
		return frameSize, nil
	}
	if err := d.decodePLCFloat32Into(pcm[:needed], frameSize); err != nil {
		return 0, err
	}
	return frameSize, nil
}

// DecodeInt16 decodes a packet into interleaved signed 16-bit PCM, or performs
// PLC when data is nil or empty. It returns the number of samples per channel
// written. For a packet, pcm must have room for its duration; for PLC, the
// per-channel request size is inferred from the whole interleaved frames in
// pcm and is capped at 120 ms. A short buffer returns ErrBufferTooSmall.
func (d *MultistreamDecoder) DecodeInt16(data []byte, pcm []int16) (int, error) {
	channels := int(d.channels)
	frameSize, err := d.decodeFrameSize(data, len(pcm))
	if err != nil {
		return 0, err
	}
	needed := frameSize * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	if len(data) == 0 {
		if handled, err := d.fixedDecodeInt16(nil, pcm[:needed], frameSize); err != nil {
			return 0, err
		} else if handled {
			return frameSize, nil
		}
		if err := d.decodePLCInt16Into(pcm[:needed], frameSize); err != nil {
			return 0, err
		}
		return frameSize, nil
	}

	if handled, err := d.fixedDecodeInt16(data, pcm, frameSize); err != nil {
		return 0, err
	} else if handled {
		d.lastFrameSize = int32(frameSize)
		return frameSize, nil
	}

	samples := d.decodeScratchFor(needed)
	n, err := d.dec.DecodeIntoFloat32(data, samples, frameSize)
	if err != nil {
		if err == multistream.ErrBufferTooSmall {
			return 0, ErrBufferTooSmall
		}
		return 0, err
	}
	total := n * channels
	softClipAndFloat32ToInt16Scalar(pcm[:total], samples[:total], n, channels, d.softClipMem)

	if len(data) > 0 {
		d.lastFrameSize = int32(frameSize)
	}

	return n, nil
}

// DecodeInt24 decodes a packet into interleaved signed 24-bit PCM stored in
// int32 values, or performs PLC when data is nil or empty. Values use the
// right-justified 24-bit PCM scale; output gain can exceed that range.
// The method returns the number of samples per channel written. For a packet,
// pcm must have room for
// its duration; for PLC, the per-channel request size is inferred from the whole
// interleaved frames in pcm and is capped at 120 ms. A short buffer returns
// ErrBufferTooSmall.
func (d *MultistreamDecoder) DecodeInt24(data []byte, pcm []int32) (int, error) {
	channels := int(d.channels)
	frameSize, err := d.decodeFrameSize(data, len(pcm))
	if err != nil {
		return 0, err
	}
	needed := frameSize * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	if len(data) == 0 {
		if handled, err := d.fixedDecodeInt24(nil, pcm[:needed], frameSize); err != nil {
			return 0, err
		} else if handled {
			return frameSize, nil
		}
		if err := d.decodePLCInt24Into(pcm[:needed], frameSize); err != nil {
			return 0, err
		}
		return frameSize, nil
	}

	if handled, err := d.fixedDecodeInt24(data, pcm, frameSize); err != nil {
		return 0, err
	} else if handled {
		clear(d.softClipMem)
		d.lastFrameSize = int32(frameSize)
		return frameSize, nil
	}

	samples := d.decodeScratchFor(needed)
	n, err := d.dec.DecodeIntoFloat32(data, samples, frameSize)
	if err != nil {
		if err == multistream.ErrBufferTooSmall {
			return 0, ErrBufferTooSmall
		}
		return 0, err
	}
	total := n * channels
	float32ToInt24Slice(pcm[:total], samples[:total], n, channels)
	clear(d.softClipMem)

	if len(data) > 0 {
		d.lastFrameSize = int32(frameSize)
	}

	return n, nil
}

// DecodeInt24Slice decodes a packet into interleaved signed 24-bit PCM stored
// in a newly allocated int32 slice. Values are right-justified. For nil or empty
// data it requests 60 ms of PLC. Use DecodeInt24 with a reusable buffer to avoid
// the output allocation.
func (d *MultistreamDecoder) DecodeInt24Slice(data []byte) ([]int32, error) {
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	// 60 ms is the maximum Opus frame duration; allocate a buffer large
	// enough for any valid packet, then trim to the actual decoded length.
	maxFrameSize := sampleRate * 60 / 1000
	pcm := make([]int32, maxFrameSize*channels)
	n, err := d.DecodeInt24(data, pcm)
	if err != nil {
		return nil, err
	}
	return pcm[:n*channels], nil
}

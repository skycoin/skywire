package multistream

// ensureStreamBuffers grows dst to hold numStreams per-stream buffers of the
// required width (frameSize*channels) and zeroes the active prefix so the
// routing pass below behaves identically to freshly allocated buffers. The
// backing slices are reused across calls to keep the encode hot path
// allocation-free; only growth past previous capacity allocates.
func ensureStreamBuffers(dst [][]float32, frameSize, coupledStreams, numStreams int) [][]float32 {
	if cap(dst) < numStreams {
		dst = make([][]float32, numStreams)
	}
	dst = dst[:numStreams]
	for i := range numStreams {
		need := frameSize * streamChannels(i, coupledStreams)
		if cap(dst[i]) < need {
			dst[i] = make([]float32, need)
		} else {
			dst[i] = dst[i][:need]
			for j := range dst[i] {
				dst[i][j] = 0
			}
		}
	}
	return dst
}

// routeChannelsToStreams routes interleaved input to stream buffers.
// This is the inverse of applyChannelMapping in multistream.go.
//
// Input format: sample-interleaved [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
// Output: slice of buffers, one per stream (stereo streams interleaved)
func routeChannelsToStreams(
	scratch [][]float32,
	input []float32,
	mapping []byte,
	coupledStreams int,
	frameSize int,
	inputChannels int,
	numStreams int,
) [][]float32 {
	streamBuffers := ensureStreamBuffers(scratch, frameSize, coupledStreams, numStreams)

	// Route input channels to appropriate streams
	// Key insight: mapping[outCh] tells us which stream channel feeds outCh
	// For encoding, we use the same mapping direction: input channel outCh
	// routes to the stream/channel specified by mapping[outCh]
	for outCh := range inputChannels {
		mappingIdx := mapping[outCh]
		if mappingIdx == 255 {
			continue // Silent channel, skip
		}

		streamIdx, chanInStream := resolveMapping(mappingIdx, coupledStreams)
		if streamIdx < 0 || streamIdx >= numStreams {
			continue
		}

		srcChannels := streamChannels(streamIdx, coupledStreams)

		// Copy samples from input channel to stream buffer
		for s := range frameSize {
			streamBuffers[streamIdx][s*srcChannels+chanInStream] = input[s*inputChannels+outCh]
		}
	}

	return streamBuffers
}

func (e *Encoder) routeInputToStreams(scratch [][]float32, pcm []float32, frameSize int) [][]float32 {
	if e.mappingFamily == 3 {
		return e.routeProjectionMixingToStreams(scratch, pcm, frameSize)
	}
	return routeChannelsToStreams(scratch, pcm, e.mapping, e.coupledStreams, frameSize, e.inputChannels, e.streams)
}

func (e *Encoder) routeProjectionMixingToStreams(scratch [][]float32, pcm []float32, frameSize int) [][]float32 {
	rows := e.projectionRows
	cols := e.projectionCols
	if len(e.projectionMixing) == 0 || rows <= 0 || cols <= 0 {
		return routeChannelsToStreams(scratch, pcm, e.mapping, e.coupledStreams, frameSize, e.inputChannels, e.streams)
	}
	if cap(e.projectionFrame) < cols {
		e.projectionFrame = make([]float32, cols)
	}
	frame := e.projectionFrame[:cols]
	streamBuffers := ensureStreamBuffers(scratch, frameSize, e.coupledStreams, e.streams)

	for s := range frameSize {
		inBase := s * cols
		for col := range cols {
			frame[col] = float32(pcm[inBase+col])
		}
		for row := 0; row < rows && row < e.inputChannels; row++ {
			mappingIdx := e.mapping[row]
			if mappingIdx == 255 {
				continue
			}
			streamIdx, chanInStream := resolveMapping(mappingIdx, e.coupledStreams)
			if streamIdx < 0 || streamIdx >= e.streams {
				continue
			}

			var sum float32
			for col := range cols {
				sum += float32(e.projectionMixing[col*rows+row]) * frame[col]
			}
			sample := (1.0 / 32768.0) * sum
			srcChannels := streamChannels(streamIdx, e.coupledStreams)
			streamBuffers[streamIdx][s*srcChannels+chanInStream] = sample
		}
	}

	return streamBuffers
}

// assembleMultistreamPacket combines individual stream packets into a multistream packet.
// Per RFC 6716 Appendix B:
//   - First N-1 packets use self-delimited packet framing
//   - Last packet uses standard framing
func (e *Encoder) assembleMultistreamPacket(streamPackets [][]byte) ([]byte, error) {
	n := len(streamPackets)
	if n == 0 {
		return nil, nil
	}

	encoded := e.assembleScratch
	if cap(encoded) < n {
		encoded = make([][]byte, n)
	}
	encoded = encoded[:n]
	e.assembleScratch = encoded

	// The first N-1 packets are reframed to self-delimited form, each growing by
	// at most 2 bytes; carve them from one reusable arena so they coexist until
	// the final copy below without per-packet allocation.
	arenaNeed := 0
	for i := 0; i < n-1; i++ {
		if len(streamPackets[i]) == 0 {
			return nil, ErrInvalidPacket
		}
		arenaNeed += len(streamPackets[i]) + 2
	}
	e.assembleArena.Ensure(arenaNeed)

	totalSize := 0
	for i, packet := range streamPackets {
		if len(packet) == 0 {
			return nil, ErrInvalidPacket
		}

		if i < n-1 {
			written, err := makeSelfDelimitedPacketInto(&e.packetParser, e.assembleArena.Tail(), packet)
			if err != nil {
				return nil, err
			}
			packet = e.assembleArena.AllocN(written)
		}
		encoded[i] = packet
		totalSize += len(packet)
	}

	// The assembled bytes are returned directly to the caller and may be
	// retained (e.g. building a packet sequence), so this buffer is freshly
	// allocated rather than reused.
	output := make([]byte, totalSize)
	offset := 0
	for _, packet := range encoded {
		copy(output[offset:], packet)
		offset += len(packet)
	}
	return output, nil
}

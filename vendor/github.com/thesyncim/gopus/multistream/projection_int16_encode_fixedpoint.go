//go:build gopus_fixed_point

package multistream

import "github.com/thesyncim/gopus/internal/encoder"

type projectionShortResFields struct {
	projectionShortResScratch [][]int32
}

func ensureProjectionShortResBuffers(dst [][]int32, frameSize, coupledStreams, numStreams int) [][]int32 {
	if cap(dst) < numStreams {
		dst = make([][]int32, numStreams)
	}
	dst = dst[:numStreams]
	for stream := range numStreams {
		n := frameSize * streamChannels(stream, coupledStreams)
		if cap(dst[stream]) < n {
			dst[stream] = make([]int32, n)
		}
		dst[stream] = dst[stream][:n]
	}
	return dst
}

// routeProjectionMixingShortToStreams follows the FIXED_POINT+ENABLE_RES24
// branch of mapping_matrix_multiply_channel_in_short (src/mapping_matrix.c).
// Each product is shifted before int32 accumulation. The exact opus_res is
// retained for the fixed encoder; float32 carries only analysis/policy input.
func (e *Encoder) routeProjectionMixingShortToStreams(scratch [][]float32, pcm []int16, frameSize int) [][]float32 {
	rows := e.projectionRows
	cols := e.projectionCols
	if e.mappingFamily != 3 || len(e.projectionMixing) < rows*cols || rows <= 0 || cols <= 0 {
		return nil
	}
	streamBuffers := ensureStreamBuffers(scratch, frameSize, e.coupledStreams, e.streams)
	e.projectionShortResScratch = ensureProjectionShortResBuffers(e.projectionShortResScratch, frameSize, e.coupledStreams, e.streams)
	const resScale = float32(1.0 / (32768.0 * 256.0))
	for sample := range frameSize {
		inputBase := sample * cols
		for row := range rows {
			mappingIdx := e.mapping[row]
			if mappingIdx == 255 {
				continue
			}
			streamIdx, chanInStream := resolveMapping(mappingIdx, e.coupledStreams)
			if streamIdx < 0 || streamIdx >= e.streams {
				continue
			}
			var sum int32
			for col := range cols {
				product := int32(e.projectionMixing[col*rows+row]) * int32(pcm[inputBase+col])
				sum += product >> 8
			}
			res := sum << 1
			srcChannels := streamChannels(streamIdx, e.coupledStreams)
			idx := sample*srcChannels + chanInStream
			e.projectionShortResScratch[streamIdx][idx] = res
			streamBuffers[streamIdx][idx] = float32(res) * resScale
		}
	}
	return streamBuffers
}

func (e *Encoder) encodeProjectionShortStream(enc *encoder.Encoder, stream int, pcm []float32, frameSize int, analysisPCM []float32, maxDataBytes int) ([]byte, error) {
	return enc.EncodeShortMixedResWithAnalysisMaxBytes(pcm, e.projectionShortResScratch[stream], frameSize, analysisPCM, maxDataBytes)
}

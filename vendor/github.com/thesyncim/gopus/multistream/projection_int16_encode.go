//go:build !gopus_fixed_point

package multistream

// routeProjectionMixingShortToStreams applies the projection encoder's short
// input callback (mapping_matrix_multiply_channel_in_short) in the float
// build. The caller retains the original int16 PCM for tonality analysis.
func (e *Encoder) routeProjectionMixingShortToStreams(scratch [][]float32, pcm []int16, frameSize int) [][]float32 {
	rows := e.projectionRows
	cols := e.projectionCols
	if e.mappingFamily != 3 || len(e.projectionMixing) < rows*cols || rows <= 0 || cols <= 0 {
		return nil
	}
	streamBuffers := ensureStreamBuffers(scratch, frameSize, e.coupledStreams, e.streams)
	for sample := range frameSize {
		inputBase := sample * cols
		for row := 0; row < rows; row++ {
			mappingIdx := e.mapping[row]
			if mappingIdx == 255 {
				continue
			}
			streamIdx, chanInStream := resolveMapping(mappingIdx, e.coupledStreams)
			if streamIdx < 0 || streamIdx >= e.streams {
				continue
			}
			srcChannels := streamChannels(streamIdx, e.coupledStreams)
			streamBuffers[streamIdx][sample*srcChannels+chanInStream] = projectionShortMixSample(
				e.projectionMixing, pcm, rows, cols, row, inputBase)
		}
	}
	return streamBuffers
}

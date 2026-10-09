//go:build !gopus_fixed_point

package multistream

import "github.com/thesyncim/gopus/internal/encoder"

type projectionShortResFields struct{}

func (e *Encoder) encodeProjectionShortStream(enc *encoder.Encoder, _ int, pcm []float32, frameSize int, analysisPCM []float32, maxDataBytes int) ([]byte, error) {
	return enc.EncodeShortMixedWithAnalysisMaxBytes(pcm, frameSize, analysisPCM, maxDataBytes)
}

// projectionShortMixSample follows the float branch of
// mapping_matrix_multiply_channel_in_short (src/mapping_matrix.c).
func projectionShortMixSample(matrix []int16, pcm []int16, rows, cols, row, inputBase int) float32 {
	var sum float32
	for col := range cols {
		product := int32(matrix[col*rows+row]) * int32(pcm[inputBase+col])
		sum += float32(product)
	}
	return (1.0 / (32768.0 * 32768.0)) * sum
}

package multistream

// applyProjectionDemixingResInt24 mirrors
// mapping_matrix_multiply_channel_out_int24: each input column is multiplied
// by its Q15 matrix coefficient, rounded, and accumulated into int24 PCM.
func applyProjectionDemixingResInt24(dst []int32, src []int32, matrix []int16, frameSize, rows, cols int) {
	clear(dst[:frameSize*rows])
	for sample := range frameSize {
		base := sample * rows
		for col := range min(rows, cols) {
			input := int64(src[base+col])
			matrixCol := matrix[col*rows : col*rows+rows]
			for row, coeff := range matrixCol {
				term := (int64(coeff)*input + 16384) >> 15
				dst[base+row] = int32(int64(dst[base+row]) + term)
			}
		}
	}
}

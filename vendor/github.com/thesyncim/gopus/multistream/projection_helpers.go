package multistream

// SetProjectionDemixingMatrix sets the optional signed 16-bit little-endian
// projection matrix in column-major order. The matrix has outputChannels rows
// and streams+coupledStreams columns, so its byte length must be
// 2*outputChannels*(streams+coupledStreams). It requires identity channel
// mapping. The matrix is copied; an empty slice clears demixing. Invalid size or
// mapping returns ErrInvalidProjectionMatrix.
func (d *Decoder) SetProjectionDemixingMatrix(matrix []byte) error {
	if len(matrix) == 0 {
		d.projectionDemixing = nil
		d.projectionCols = 0
		return nil
	}

	rows := d.outputChannels
	cols := d.streams + d.coupledStreams
	if rows <= 0 || cols <= 0 {
		return ErrInvalidProjectionMatrix
	}
	if len(matrix) != 2*rows*cols {
		return ErrInvalidProjectionMatrix
	}

	// Projection family decoders use trivial channel mapping.
	for i := range rows {
		if d.mapping[i] != byte(i) {
			return ErrInvalidProjectionMatrix
		}
	}

	needed := rows * cols
	if cap(d.projectionDemixing) < needed {
		d.projectionDemixing = make([]int16, needed)
	}
	coeffs := d.projectionDemixing[:needed]
	for i := range needed {
		coeffs[i] = int16(uint16(matrix[2*i]) | (uint16(matrix[2*i+1]) << 8))
	}
	d.projectionCols = cols
	return nil
}

func (d *Decoder) applyProjectionDemixing32(output []float32, frameSize int) {
	rows := d.outputChannels
	cols := d.projectionCols
	if len(d.projectionDemixing) == 0 || cols <= 0 || rows <= 0 {
		return
	}

	if cap(d.projectionScratch) < cols {
		d.projectionScratch = make([]float32, cols)
	}
	applyProjectionDemixingMatrix32(output, output, d.projectionDemixing, d.projectionScratch[:cols], frameSize, rows, cols)
}

func (d *Decoder) applyProjectionDemixingInt16(output []int16, input []float32, frameSize int) {
	rows := d.outputChannels
	cols := d.projectionCols
	if len(d.projectionDemixing) == 0 || cols <= 0 || rows <= 0 {
		copy(output, float32ToInt16(input))
		return
	}

	if cap(d.projectionScratch) < cols {
		d.projectionScratch = make([]float32, cols)
	}
	applyProjectionDemixingMatrixInt16(output, input, d.projectionDemixing, d.projectionScratch[:cols], frameSize, rows, cols)
}

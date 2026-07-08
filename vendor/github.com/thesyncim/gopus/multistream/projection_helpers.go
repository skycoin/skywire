package multistream

// SetProjectionDemixingMatrix sets optional projection demixing coefficients.
// Matrix data is S16LE, column-major, with dimensions:
//
//	rows = output channels
//	cols = streams + coupledStreams
//
// This method is intended for mapping-family-3 projection flows where
// decoded stream channels are routed with trivial mapping and then demixed
// to output channels.
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
	if len(d.projectionDemixing) == 0 || cols <= 0 || rows <= 0 || cols > rows {
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
	if len(d.projectionDemixing) == 0 || cols <= 0 || rows <= 0 || cols > rows {
		copy(output, float32ToInt16(input))
		return
	}

	if cap(d.projectionScratch) < cols {
		d.projectionScratch = make([]float32, cols)
	}
	applyProjectionDemixingMatrixInt16(output, input, d.projectionDemixing, d.projectionScratch[:cols], frameSize, rows, cols)
}

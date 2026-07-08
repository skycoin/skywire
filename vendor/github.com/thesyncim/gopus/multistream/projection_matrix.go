package multistream

import "github.com/thesyncim/gopus/internal/opusmath"

func applyProjectionDemixingMatrix32(dst, src []float32, matrix []int16, frame []float32, frameSize, rows, cols int) {
	frame = frame[:cols]
	for s := range frameSize {
		inBase := s * rows
		outBase := s * rows
		in := src[inBase : inBase+rows]
		out := dst[outBase : outBase+rows]
		for col := range cols {
			frame[col] = in[col]
		}
		for row := range out {
			out[row] = 0
		}
		for col := range cols {
			inputSample := frame[col]
			mcol := matrix[col*rows : col*rows+rows]
			for row := range rows {
				tmp := float32(mcol[row]) * inputSample * (1.0 / 32768.0)
				out[row] += tmp
			}
		}
	}
}

func applyProjectionDemixingMatrixInt16(dst []int16, src []float32, matrix []int16, frame []float32, frameSize, rows, cols int) {
	frame = frame[:cols]
	for s := range frameSize {
		inBase := s * rows
		outBase := s * rows
		in := src[inBase : inBase+rows]
		out := dst[outBase : outBase+rows]
		for col := range cols {
			frame[col] = in[col]
		}
		for col := range cols {
			inputSample := int32(opusmath.Float32ToInt16(frame[col]))
			mcol := matrix[col*rows : col*rows+rows]
			for row := range rows {
				tmp := int32(mcol[row]) * inputSample
				out[row] = int16(int32(out[row]) + ((tmp + 16384) >> 15))
			}
		}
	}
}

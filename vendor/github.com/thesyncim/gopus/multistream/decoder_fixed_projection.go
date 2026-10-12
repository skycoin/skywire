//go:build gopus_fixed_point

package multistream

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/fixedpoint"
)

const projectionFixedResToFloat32 = float32(1.0 / 8388608.0)

// decodeProjectionResFixed decodes the elementary streams through their
// integer opus_res paths and returns the input-channel rows before projection
// demixing. Family-3 layouts use identity channel routing, so the ordinary
// fixed decoder's mapped output has the same column order as the projection
// matrix. The matrix is hidden only for this nested decode; the Decoder is not
// reentrant, and the original matrix is restored on every return path.
func (d *Decoder) decodeProjectionResFixed(data []byte, frameSize int) ([]int32, bool, error) {
	if len(d.projectionDemixing) == 0 || d.projectionCols <= 0 {
		return nil, false, nil
	}
	demixing := d.projectionDemixing
	cols := d.projectionCols
	d.projectionDemixing = nil
	d.projectionCols = 0
	defer func() {
		d.projectionDemixing = demixing
		d.projectionCols = cols
	}()
	return d.DecodeToResFixed(data, frameSize)
}

func (d *Decoder) projectionDecodeDuration(data []byte, frameSize int) (int, error) {
	if frameSize <= 0 {
		return 0, ErrInvalidPacket
	}
	frameSize = min(frameSize, int(d.sampleRate)*3/25)
	if len(data) == 0 {
		return frameSize, nil
	}
	packets, err := parseMultistreamPacketScratch(d.packetsScratch, &d.packetParser, &d.reframeArena, data, d.streams)
	if err != nil {
		return 0, fmt.Errorf("multistream: parse error: %w", err)
	}
	d.packetsScratch = packets
	duration, err := validateStreamDurationsAtRateScratch(&d.packetParser, packets, int(d.sampleRate))
	if err != nil {
		return 0, err
	}
	if duration > frameSize {
		return 0, ErrBufferTooSmall
	}
	return duration, nil
}

func (d *Decoder) decodeFixedProjectionFloat32(data []byte, output []float32, frameSize int) (int, bool, error) {
	if len(d.projectionDemixing) == 0 || d.projectionCols <= 0 {
		return 0, false, nil
	}
	duration, err := d.projectionDecodeDuration(data, frameSize)
	if err != nil {
		return 0, true, err
	}
	needed := duration * d.outputChannels
	if len(output) < needed {
		return 0, true, ErrBufferTooSmall
	}
	res, handled, err := d.decodeProjectionResFixed(data, frameSize)
	if err != nil || !handled {
		return 0, handled, err
	}
	if len(res) != needed {
		return 0, false, fmt.Errorf("multistream: fixed projection decode returned %d opus_res samples, want %d", len(res), needed)
	}
	applyProjectionDemixingResFloat32(output[:needed], res, d.projectionDemixing, duration, d.outputChannels, d.projectionCols)
	return duration, true, nil
}

func (d *Decoder) decodeFixedProjectionInt16(data []byte, frameSize int) ([]int16, bool, error) {
	if len(d.projectionDemixing) == 0 || d.projectionCols <= 0 {
		return nil, false, nil
	}
	res, handled, err := d.decodeProjectionResFixed(data, frameSize)
	if err != nil || !handled {
		return nil, handled, err
	}
	rows := d.outputChannels
	if rows <= 0 || len(res)%rows != 0 {
		return nil, false, fmt.Errorf("multistream: fixed projection input has %d samples for %d rows", len(res), rows)
	}
	pcm := make([]int16, len(res))
	applyProjectionDemixingResInt16(pcm, res, d.projectionDemixing, len(res)/rows, rows, d.projectionCols)
	return pcm, true, nil
}

func (d *Decoder) decodeFixedProjectionInt24(data []byte, frameSize int) ([]int32, bool, error) {
	if len(d.projectionDemixing) == 0 || d.projectionCols <= 0 {
		return nil, false, nil
	}
	res, handled, err := d.decodeProjectionResFixed(data, frameSize)
	if err != nil || !handled {
		return nil, handled, err
	}
	rows := d.outputChannels
	if rows <= 0 || len(res)%rows != 0 {
		return nil, false, fmt.Errorf("multistream: fixed projection input has %d samples for %d rows", len(res), rows)
	}
	pcm := make([]int32, len(res))
	applyProjectionDemixingResInt24(pcm, res, d.projectionDemixing, len(res)/rows, rows, d.projectionCols)
	return pcm, true, nil
}

func (d *Decoder) decodeFixedOutputInt24(data []byte, frameSize int) ([]int32, bool, error) {
	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		return nil, false, nil
	}
	res, handled, err := d.DecodeToResFixed(data, frameSize)
	if err != nil || !handled {
		return nil, handled, err
	}
	pcm := make([]int32, len(res))
	copy(pcm, res)
	return pcm, true, nil
}

// applyProjectionDemixingResFloat32 mirrors mapping_matrix_multiply_channel_out_float
// for FIXED_POINT opus_res. The per-stream samples are converted with
// RES2FLOAT before the matrix coefficient is applied.
func applyProjectionDemixingResFloat32(dst []float32, src []int32, matrix []int16, frameSize, rows, cols int) {
	clear(dst[:frameSize*rows])
	const coeffScale = float32(1.0 / 32768.0)
	for sample := range frameSize {
		base := sample * rows
		for col := range min(rows, cols) {
			input := float32(src[base+col]) * projectionFixedResToFloat32
			matrixCol := matrix[col*rows : col*rows+rows]
			for row, coeff := range matrixCol {
				// Keep C's operation order: RES2FLOAT scales the input first,
				// then the Q15 coefficient is applied. The explicit float32
				// conversion preserves libopus' separate multiply/add rounding.
				term := float32(input * (float32(coeff) * coeffScale))
				dst[base+row] += term
			}
		}
	}
}

// applyProjectionDemixingResInt16 mirrors mapping_matrix_multiply_channel_out_short:
// each raw opus_res sample is converted with RES2INT16, then its matrix product
// is rounded in Q15 before it is added to the destination sample.
func applyProjectionDemixingResInt16(dst []int16, src []int32, matrix []int16, frameSize, rows, cols int) {
	clear(dst[:frameSize*rows])
	for sample := range frameSize {
		base := sample * rows
		for col := range min(rows, cols) {
			input := int32(fixedpoint.Res2Int16(src[base+col]))
			matrixCol := matrix[col*rows : col*rows+rows]
			for row, coeff := range matrixCol {
				term := (int32(coeff)*input + 16384) >> 15
				dst[base+row] = int16(int32(dst[base+row]) + term)
			}
		}
	}
}

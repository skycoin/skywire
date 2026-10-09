//go:build gopus_fixed_point

package multistream

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/fixedpoint"
)

const fixedOutputFloat32Scale = float32(1.0 / 8388608.0)

// decodeFixedOutputFloat32 mirrors the FIXED_POINT opus_multistream_decode_float
// path for packets covered by DecodeToResFixed. The selected libopus build
// converts mapped opus_res samples to float with RES2FLOAT (1/2^23).
func (d *Decoder) decodeFixedOutputFloat32(data []byte, output []float32, frameSize int) (int, bool, error) {
	if frameSize <= 0 {
		return 0, false, nil
	}
	frameSize = min(frameSize, int(d.sampleRate)*3/25)
	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		return d.decodeFixedProjectionFloat32(data, output, frameSize)
	}
	if len(data) == 0 {
		needed := frameSize * d.outputChannels
		if len(output) < needed {
			return 0, false, ErrBufferTooSmall
		}
		res, handled, err := d.DecodePLCToResFixed(frameSize)
		if err != nil || !handled {
			return 0, handled, err
		}
		if len(res) != needed {
			return 0, false, fmt.Errorf("multistream: fixed PLC returned %d opus_res samples, want %d", len(res), needed)
		}
		for i, sample := range res {
			output[i] = float32(sample) * fixedOutputFloat32Scale
		}
		return frameSize, true, nil
	}

	// Determine the packet duration and validate caller capacity before the
	// fixed decoder advances any per-stream state. DecodeToResFixed repeats this
	// parse as part of its own all-stream preflight.
	packets, err := parseMultistreamPacketScratch(d.packetsScratch, &d.packetParser, &d.reframeArena, data, d.streams)
	if err != nil {
		return 0, false, fmt.Errorf("multistream: parse error: %w", err)
	}
	d.packetsScratch = packets
	duration, err := validateStreamDurationsAtRateScratch(&d.packetParser, packets, int(d.sampleRate))
	if err != nil {
		return 0, false, err
	}
	if duration > frameSize {
		return 0, false, ErrBufferTooSmall
	}
	needed := duration * d.outputChannels
	if len(output) < needed {
		return 0, false, ErrBufferTooSmall
	}

	res, handled, err := d.DecodeToResFixed(data, frameSize)
	if err != nil {
		return 0, false, err
	}
	if !handled {
		return 0, false, nil
	}
	if len(res) != needed {
		return 0, false, fmt.Errorf("multistream: fixed decode returned %d opus_res samples, want %d", len(res), needed)
	}
	for i, sample := range res {
		output[i] = float32(sample) * fixedOutputFloat32Scale
	}
	return duration, true, nil
}

// decodeFixedOutputInt16 mirrors the FIXED_POINT opus_multistream_decode path
// by converting mapped opus_res samples with RES2INT16.
func (d *Decoder) decodeFixedOutputInt16(data []byte, frameSize int) ([]int16, bool, error) {
	if frameSize <= 0 {
		return nil, false, nil
	}
	frameSize = min(frameSize, int(d.sampleRate)*3/25)
	res, handled, err := d.DecodeToResFixed(data, frameSize)
	if err != nil || !handled {
		return nil, handled, err
	}
	pcm := make([]int16, len(res))
	for i, sample := range res {
		pcm[i] = fixedpoint.Res2Int16(sample)
	}
	return pcm, true, nil
}

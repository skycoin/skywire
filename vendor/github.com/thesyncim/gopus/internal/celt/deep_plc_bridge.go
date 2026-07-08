//go:build gopus_dred || gopus_osce

package celt

import "github.com/thesyncim/gopus/internal/opusmath"

const (
	plcUpdateFrames    = 4
	plcUpdateFrameSize = 160
	plcUpdateSamples   = plcUpdateFrames * plcUpdateFrameSize
	plcUpdateSincOrder = 48
	plcUpdateOffset    = plcDecodeBufferSize - plcUpdateSincOrder - 1 - 3*(plcUpdateSamples-1)
	// Matches libopus dnn/freq.h PREEMPHASIS used by deep PLC, distinct
	// from CELT's mode preemphasis coefficient.
	deepPLCPreemphCoef = float32(0.85)
)

// Matches libopus celt_decoder.c update_plc_state() sinc_filter.
var plcUpdateSincFilter = [...]float32{
	4.2931e-05, -0.000190293, -0.000816132, -0.000637162, 0.00141662, 0.00354764, 0.00184368, -0.00428274,
	-0.00856105, -0.0034003, 0.00930201, 0.0159616, 0.00489785, -0.0169649, -0.0259484, -0.00596856,
	0.0286551, 0.0405872, 0.00649994, -0.0509284, -0.0716655, -0.00665212, 0.134336, 0.278927,
	0.339995, 0.278927, 0.134336, -0.00665212, -0.0716655, -0.0509284, 0.00649994, 0.0405872,
	0.0286551, -0.00596856, -0.0259484, -0.0169649, 0.00489785, 0.0159616, 0.00930201, -0.0034003,
	-0.00856105, -0.00428274, 0.00184368, 0.00354764, 0.00141662, -0.000637162, -0.000816132, -0.000190293,
	4.2931e-05,
}

// FillPLCUpdate16kMonoWithPreemphasisMem mirrors the 48 kHz -> 16 kHz history
// downsample libopus uses in update_plc_state() before the first neural PLC
// frame. The output is quantized to the same int16 grid lpcnet_plc_update()
// receives, and the returned preemphasis memory matches libopus's retained
// plc_preemphasis_mem after the history prefilter pass.
func (d *Decoder) FillPLCUpdate16kMonoWithPreemphasisMem(dst []float32) (int, float32) {
	if d == nil || len(dst) < plcUpdateSamples || d.channels <= 0 {
		return 0, 0
	}
	channels := int(d.channels)
	if len(d.plcDecodeMem) < plcDecodeBufferSize*channels {
		return 0, 0
	}
	d.materializePLCDecodeHistory()

	buf48k := ensureFloat32Slice(&d.scratchPLCUpdate48k, plcDecodeBufferSize)
	if d.channels == 1 {
		hist := d.plcDecodeMem[:plcDecodeBufferSize]
		for i := 0; i < plcDecodeBufferSize; i++ {
			buf48k[i] = float32(hist[i])
		}
	} else {
		histL := d.plcDecodeMem[:plcDecodeBufferSize]
		histR := d.plcDecodeMem[plcDecodeBufferSize : 2*plcDecodeBufferSize]
		for i := 0; i < plcDecodeBufferSize; i++ {
			buf48k[i] = float32(0.5 * (histL[i] + histR[i]))
		}
	}

	preemph := deepPLCPreemphCoef
	for i := 1; i < plcDecodeBufferSize; i++ {
		buf48k[i] += preemph * buf48k[i-1]
	}
	preemphMem := buf48k[plcDecodeBufferSize-1]

	for i := 0; i < plcUpdateSamples; i++ {
		sum := float32(0)
		base := 3*i + plcUpdateOffset
		for j := 0; j <= plcUpdateSincOrder; j++ {
			sum += buf48k[base+j] * plcUpdateSincFilter[j]
		}
		dst[i] = quantizedRawPCM16GridSample(sum)
	}
	return plcUpdateSamples, preemphMem * (1.0 / 32768.0)
}

// FillPLCUpdate16kMono mirrors the 48 kHz -> 16 kHz history downsample libopus
// uses in update_plc_state() before the first neural PLC frame. The output is
// quantized to the same int16 grid lpcnet_plc_update() receives.
func (d *Decoder) FillPLCUpdate16kMono(dst []float32) int {
	n, _ := d.FillPLCUpdate16kMonoWithPreemphasisMem(dst)
	return n
}

func quantizeRawPCM16LikeInt16(sample float32) int16 {
	if sample < -32767 {
		sample = -32767
	}
	if sample > 32767 {
		sample = 32767
	}
	return opusmath.Float32ToInt16Raw(sample)
}

func quantizedRawPCM16GridSample(sample float32) float32 {
	return float32(quantizeRawPCM16LikeInt16(sample)) * (1.0 / 32768.0)
}

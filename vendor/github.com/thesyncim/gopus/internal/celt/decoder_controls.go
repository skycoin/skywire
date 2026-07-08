package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// SetRangeDecoder sets the range decoder for the current frame.
// This must be called before decoding each frame.
func (d *Decoder) SetRangeDecoder(rd *rangecoding.Decoder) {
	d.rangeDecoder = rd
}

// RangeDecoder returns the current range decoder.
func (d *Decoder) RangeDecoder() *rangecoding.Decoder {
	return d.rangeDecoder
}

// FinalRange returns the final range coder state after decoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after decoding a frame to get a meaningful value.
func (d *Decoder) FinalRange() uint32 {
	return d.rng
}

func (d *Decoder) takeQEXTPayload() []byte {
	if !extsupport.QEXT {
		return nil
	}
	state := d.qextState()
	if state == nil {
		return nil
	}
	payload := state.pendingPayload
	state.pendingPayload = nil
	return payload
}

func (d *Decoder) decodeFineEnergyGLogWithDecoderPrev(rd *rangecoding.Decoder, energies []celtGLog, nbBands int, prevQuant, extraQuant []int32) {
	if rd == nil {
		return
	}
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	d.decodeFineEnergyGLog(energies, nbBands, prevQuant, extraQuant)
	d.rangeDecoder = oldRD
}

func combineFinalRange(mainRD, extRD *rangecoding.Decoder) uint32 {
	if mainRD == nil {
		return 0
	}
	rng := mainRD.Range()
	if extsupport.QEXT && extRD != nil {
		rng ^= extRD.Range()
	}
	return rng
}

// Channels returns the number of audio channels (1 or 2).
func (d *Decoder) Channels() int {
	return int(d.channels)
}

// SetBandwidth sets the CELT bandwidth derived from the Opus TOC.
func (d *Decoder) SetBandwidth(bw CELTBandwidth) {
	d.bandwidth = bw
}

// Bandwidth returns the current CELT bandwidth setting.
func (d *Decoder) Bandwidth() CELTBandwidth {
	return d.bandwidth
}

// SetComplexity sets decoder complexity (0-10).
func (d *Decoder) SetComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return ErrInvalidComplexity
	}
	d.complexity = int32(complexity)
	return nil
}

// Complexity returns the current decoder complexity setting.
func (d *Decoder) Complexity() int {
	return int(d.complexity)
}

// SampleRate returns the configured API output sample rate.
func (d *Decoder) SampleRate() int {
	return int(d.sampleRate)
}

// SetAPISampleRate sets the Opus API sample rate used by CELT downsampling.
func (d *Decoder) SetAPISampleRate(sampleRate int) error {
	switch sampleRate {
	case 48000:
		d.sampleRate = int32(sampleRate)
		d.downsample = 1
	case 24000:
		d.sampleRate = int32(sampleRate)
		d.downsample = 2
	case 16000:
		d.sampleRate = int32(sampleRate)
		d.downsample = 3
	case 12000:
		d.sampleRate = int32(sampleRate)
		d.downsample = 4
	case 8000:
		d.sampleRate = int32(sampleRate)
		d.downsample = 6
	default:
		return ErrInvalidSampleRate
	}
	return nil
}

// SetDownsample sets the libopus CELT downsample factor used for lower-rate
// Opus decoder APIs. Invalid factors fall back to the 48 kHz path.
func (d *Decoder) SetDownsample(factor int) {
	switch factor {
	case 1, 2, 3, 4, 6:
		d.downsample = int32(factor)
		d.sampleRate = int32(48000 / factor)
	default:
		d.downsample = 1
		d.sampleRate = 48000
	}
}

func (d *Decoder) downsampleFactor() int {
	if d == nil || d.downsample <= 0 {
		return 1
	}
	return int(d.downsample)
}

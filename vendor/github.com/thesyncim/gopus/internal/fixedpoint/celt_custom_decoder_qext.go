//go:build gopus_fixed_point && gopus_qext && gopus_custom_modes

package fixedpoint

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/celt"
)

// NewQEXTCELTDecoderCustom creates a fixed-point QEXT CELT decoder for a
// generated custom mode. Main-band geometry and allocation tables come from
// mode; the side-band mode exists only for the geometries initialized by
// celt/modes.c compute_qext_mode().
func NewQEXTCELTDecoderCustom(channels int, mode CELTCustomMode) (*QEXTCELTDecoder, error) {
	if channels < 1 || channels > 2 {
		return nil, fmt.Errorf("QEXT CELT channels must be 1 or 2, got %d", channels)
	}
	if mode.Fs < 8000 || mode.Fs > 96000 || mode.FrameSize <= 0 || mode.ShortMdctSize <= 0 ||
		mode.FrameSize != mode.ShortMdctSize<<mode.MaxLM || mode.Overlap < 0 ||
		len(mode.EBands) < 2 || mode.EffEBands <= 0 || mode.EffEBands >= len(mode.EBands) ||
		len(mode.LogN) < len(mode.EBands)-1 {
		return nil, fmt.Errorf("invalid custom QEXT CELT geometry %d/%d", mode.Fs, mode.FrameSize)
	}
	mdct := NewQEXTMDCTLookup(2*mode.FrameSize, mode.MaxLM, mode.Overlap)
	if mdct == nil {
		return nil, fmt.Errorf("unsupported custom QEXT CELT transform %d/%d", mode.FrameSize, mode.MaxLM)
	}

	nbEBands := len(mode.EBands) - 1
	var customTables fixedCustomTables
	if !mode.ScaledBandFamily {
		customTables = celt.NewFixedCustomTables(nbEBands, mode.ShortMdctSize, mode.MaxLM,
			mode.EBands, mode.LogN, mode.AllocVectors, mode.CacheIndex, mode.CacheBits, mode.CacheCaps)
		if customTables == nil {
			return nil, fmt.Errorf("unsupported custom QEXT CELT band tables %d", nbEBands)
		}
	}

	var qextEdges, qextLogN []int16
	qextMaxBands := 0
	if mode.Fs == 48000 || mode.Fs == 96000 {
		_, qextEdges, qextLogN, qextMaxBands, _ = fixedQEXTBandMode(mode.Fs, mode.ShortMdctSize)
	}
	decodeBufSize := qextCELTDecodeBufferSize48
	if mode.Fs == 96000 && (mode.ShortMdctSize == 240 || mode.ShortMdctSize == 180) {
		// celt_decoder.c sets qext_scale to 2 only for these native 96 kHz
		// short transforms. Other custom 96 kHz modes keep the base buffer.
		decodeBufSize = qextCELTDecodeBufferSize96
	}
	deemph0, deemph1, _, deemph3 := customPreemph(mode.Fs)
	if mode.Fs == 96000 {
		// celt/modes.c ENABLE_QEXT uses these FIXED_POINT coefficients for
		// 96 kHz custom modes (the non-QEXT customPreemph table is 48 kHz-based).
		deemph0, deemph1, deemph3 = 30245, 7209, 5415
	}
	return newQEXTCELTDecoderState(channels, mode.Fs, 1, mode.ShortMdctSize,
		mode.Overlap, decodeBufSize, mode.MaxLM, nbEBands, mode.EffEBands,
		mode.EBands, mode.LogN, qextEdges, qextLogN, qextMaxBands,
		mdct, mdct.Window(), customTables, deemph0, deemph1, deemph3)
}

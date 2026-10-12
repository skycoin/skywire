//go:build gopus_qext

package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// QEXTDecodeHeader is the decoded prefix of one side payload.
type QEXTDecodeHeader struct {
	EndBands   int
	Intensity  int
	DualStereo bool
}

// QEXTBandTablesExport exposes the static extension-mode band layout to the
// integer CELT decoder. The returned slices refer to package tables and must
// be treated as read-only.
func QEXTBandTablesExport(sampleRate, shortMDCTSize int) (edges, logN []int, ok bool) {
	cfg, ok := computeQEXTModeConfig(sampleRate, shortMDCTSize)
	if !ok {
		return nil, nil, false
	}
	return cfg.EBands, cfg.LogN, true
}

// QEXTBitsToPulsesExport returns the pulse-cache entry for one QEXT band.
func QEXTBitsToPulsesExport(band, lm, bitsQ3 int) int {
	cache, ok := pulseCacheForBandTables(band, lm, qextCacheIndex50[:], qextCacheBits50[:], nbQEXTBands)
	if !ok || bitsQ3 <= 0 {
		return 0
	}
	return bitsToPulsesCached(cache, bitsQ3)
}

// QEXTPulsesToBitsExport returns the cached Q3 cost for one QEXT pulse count.
func QEXTPulsesToBitsExport(band, lm, pulses int) int {
	cache, ok := pulseCacheForBandTables(band, lm, qextCacheIndex50[:], qextCacheBits50[:], nbQEXTBands)
	if !ok {
		return 0
	}
	return pulsesToBitsCached(cache, pulses)
}

// QEXTMaxPulsesBitsExport returns the maximum coded-pulse cost for one QEXT
// band and LM, or -1 when the cache has no entry.
func QEXTMaxPulsesBitsExport(band, lm int) int {
	cache, ok := pulseCacheForBandTables(band, lm, qextCacheIndex50[:], qextCacheBits50[:], nbQEXTBands)
	if !ok {
		return -1
	}
	return pulseCacheMaxBits(cache)
}

// QEXTEncodeDepthExport exposes the shared depth-symbol coder to fixed-point
// QEXT allocation without duplicating its stateful ICDF coding rules.
func QEXTEncodeDepthExport(enc *rangecoding.Encoder, depth, cap int32, last *int32) {
	if last == nil {
		return
	}
	lastInt := int(*last)
	encodeQEXTDepth(enc, int(depth), int(cap), &lastInt)
	*last = int32(lastInt)
}

// QEXTDecodeHeaderExport exposes the side-payload prefix parser to the fixed
// point decoder.
func QEXTDecodeHeaderExport(dec *rangecoding.Decoder, channels, totalBytes int) QEXTDecodeHeader {
	h := decodeQEXTHeader(dec, channels, totalBytes)
	return QEXTDecodeHeader(h)
}

// QEXTDecodeExtraAllocationExport exposes the selected mode's shared
// decode-side depth allocation to the fixed-point CELT driver. Extra QEXT band
// entries remain at [MaxBands, MaxBands+qextEnd), matching the float decoder.
func QEXTDecodeExtraAllocationExport(start, end, qextEnd, totalQ3, channels, lm int,
	dec *rangecoding.Decoder, extraPulses, extraQuant []int32, sampleRate, shortMDCTSize int,
) bool {
	var mode *qextModeConfig
	if qextEnd > 0 {
		cfg, ok := computeQEXTModeConfig(sampleRate, shortMDCTSize)
		if !ok {
			return false
		}
		cfg.EffBands = min(qextEnd, cfg.EffBands)
		mode = &cfg
	}
	computeQEXTExtraAllocationDecodeWithMode(start, end, qextEnd, totalQ3, channels, lm,
		dec, extraPulses, extraQuant, mode)
	return true
}

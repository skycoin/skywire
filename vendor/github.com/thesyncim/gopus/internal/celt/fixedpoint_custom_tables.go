//go:build gopus_fixed_point && gopus_custom_modes

package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// FixedCustomTables retains a libopus CELTMode's allocation and pulse tables
// for the fixed-point custom encoder and decoder. It is built once per mode.
type FixedCustomTables struct{ mode *perModeTables }

func NewFixedCustomTables(nbEBands, scaleBase, maxLM int, eBands, logN []int16, allocVectors []uint8, cacheIndex []int16, cacheBits, cacheCaps []uint8) *FixedCustomTables {
	if nbEBands <= 0 || nbEBands > MaxCustomBands || scaleBase <= 0 || maxLM < 0 || maxLM > 3 ||
		len(eBands) < nbEBands+1 || len(logN) < nbEBands || len(allocVectors) < 11*nbEBands ||
		len(cacheIndex) < (maxLM+2)*nbEBands || len(cacheCaps) < (maxLM+1)*2*nbEBands {
		return nil
	}
	mode := buildPerModeTables(nbEBands, scaleBase, eBands, logN, allocVectors, cacheIndex, cacheBits, cacheCaps)
	if mode == nil {
		return nil
	}
	return &FixedCustomTables{mode: mode}
}

func (t *FixedCustomTables) InitCapsInto(caps []int32, bands, lm, channels int) {
	initCapsIntoMode(caps, bands, lm, channels, t.mode)
}

func (t *FixedCustomTables) BitsToPulses(band, lm, bitsQ3 int) int {
	cache, ok := pulseCacheForBandTables(band, lm, t.mode.cacheIndex, t.mode.cacheBits, t.mode.nbEBands)
	if !ok {
		return 0
	}
	return bitsToPulsesCached(cache, bitsQ3)
}

func (t *FixedCustomTables) PulsesToBits(band, lm, pulses int) int {
	cache, ok := pulseCacheForBandTables(band, lm, t.mode.cacheIndex, t.mode.cacheBits, t.mode.nbEBands)
	if !ok {
		return 0
	}
	return pulsesToBitsCached(cache, pulses)
}

func (t *FixedCustomTables) MaxPulsesBits(band, lm int) int {
	cache, ok := pulseCacheForBandTables(band, lm, t.mode.cacheIndex, t.mode.cacheBits, t.mode.nbEBands)
	if !ok {
		return -1
	}
	return int(cache.bits[cache.bits[0]])
}

func (t *FixedCustomTables) ComputeAllocationWithEncoderStartInto(sc *AllocEncodeScratch, re *rangecoding.Encoder,
	start, totalBitsQ3, bands, channels int, caps, offsets []int32, trim, intensity int,
	dualStereo bool, lm, prev, signalBandwidth int) *AllocationResult {
	result := &sc.result
	result.BandBits = ensureInt32Slice(&sc.bandBits, bands)
	result.FineBits = ensureInt32Slice(&sc.fineBits, bands)
	result.FinePriority = ensureInt32Slice(&sc.finePriority, bands)
	result.Caps = ensureInt32Slice(&sc.caps, bands)
	clear(result.BandBits)
	clear(result.FineBits)
	clear(result.FinePriority)
	copy(result.Caps, caps[:bands])
	result.Balance = 0
	result.CodedBands = bands
	result.Intensity = intensity
	result.DualStereo = dualStereo
	iv, dv := intensity, 0
	if dualStereo {
		dv = 1
	}
	work := ensureInt32Slice(&sc.modeWork, bands*5)
	result.CodedBands = cltComputeAllocationWithScratchModeEncode(re, start, bands, offsets, caps,
		trim, &iv, &dv, totalBitsQ3, &result.Balance, result.BandBits, result.FineBits,
		result.FinePriority, channels, lm, prev, signalBandwidth, work, t.mode)
	result.Intensity = iv
	result.DualStereo = dv != 0
	return result
}

func (t *FixedCustomTables) DecodeCELTAllocation(rd *rangecoding.Decoder,
	totalBits, start, end, lm, channels int, transient bool) CELTDecodeAllocation {
	a := CELTDecodeAllocation{Spread: spreadNormal}
	tfDecode(start, end, transient, a.TFRes[:end], lm, rd)
	tell := rd.Tell()
	if tell+4 <= totalBits {
		a.Spread = rd.DecodeICDF(spreadICDF, 5)
	}
	caps := a.Caps[:end]
	initCapsIntoMode(caps, end, lm, channels, t.mode)
	offsets := a.Offsets[:end]
	totalBitsQ3, tellFrac := decodeDynallocOffsets(rd, offsets, caps, t.mode.eBands, start, end, lm, channels, totalBits<<bitRes)
	a.AllocTrim = 5
	if tellFrac+(6<<bitRes) <= totalBitsQ3 {
		a.AllocTrim = rd.DecodeICDF(trimICDF, 7)
	}
	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		a.AntiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= a.AntiCollapseRsv
	var work [MaxCustomBands * 5]int32
	a.CodedBands = cltComputeAllocationWithScratchMode(start, end, offsets, caps, a.AllocTrim,
		&a.Intensity, &a.DualStereo, bitsQ3, &a.Balance, a.Pulses[:end], a.FineQuant[:end],
		a.FinePriority[:end], channels, lm, rd, work[:], t.mode)
	return a
}

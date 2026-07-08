package celt

import "unsafe"

const (
	maxPseudo            = 40
	logMaxPseudo         = 6
	pulseCacheLookupBits = 256
)

type pulseCacheLookup50Data struct {
	lut     [len(cacheBits50)][pulseCacheLookupBits]uint8
	maxBits [len(cacheBits50)]uint8
	valid   [len(cacheBits50)]bool
}

var (
	pulseCacheLookup50 = buildPulseCacheLookup50()
	cacheBits50Base    = uintptr(unsafe.Pointer(&cacheBits50[0]))
	cacheBits50End     = cacheBits50Base + uintptr(len(cacheBits50))
)

func getPulses(i int) int {
	if i < 8 {
		return i
	}
	return (8 + (i & 7)) << ((i >> 3) - 1)
}

func pulseCacheForBand(band, lm int) ([]uint8, bool) {
	if band < 0 || band >= MaxBands {
		return nil, false
	}
	if lm < -1 {
		return nil, false
	}
	idx := (lm + 1) * MaxBands
	if idx < 0 || idx >= len(cacheIndex50) {
		return nil, false
	}
	start := int(cacheIndex50[idx+band])
	if start < 0 || start >= len(cacheBits50) {
		return nil, false
	}
	cache := cacheBits50[start:]
	if len(cache) == 0 {
		return nil, false
	}
	maxPseudo := int(cache[0])
	if maxPseudo <= 0 || maxPseudo >= len(cache) {
		return nil, false
	}
	return cache, true
}

func bitsToPulses(band, lm, bitsQ3 int) int {
	if bitsQ3 <= 0 {
		return 0
	}
	cache, ok := pulseCacheForBand(band, lm)
	if !ok {
		return 0
	}
	return bitsToPulsesCached(cache, bitsQ3)
}

func pulsesToBits(band, lm, pulses int) int {
	if pulses <= 0 {
		return 0
	}
	cache, ok := pulseCacheForBand(band, lm)
	if !ok {
		return 0
	}
	return pulsesToBitsCached(cache, pulses)
}

func bitsToPulsesCached(cache []uint8, bitsQ3 int) int {
	if bitsQ3 <= 0 || len(cache) == 0 {
		return 0
	}
	return bitsToPulsesCachedFast(cache, bitsQ3)
}

func pulsesToBitsCached(cache []uint8, pulses int) int {
	if pulses <= 0 || len(cache) == 0 {
		return 0
	}
	maxPseudo := int(cache[0])
	if pulses > maxPseudo {
		pulses = maxPseudo
	}
	return int(cache[pulses]) + 1
}

func pulseCacheMaxBits(cache []uint8) int {
	if len(cache) == 0 {
		return 0
	}
	maxPseudo := int(cache[0])
	if maxPseudo <= 0 || maxPseudo >= len(cache) {
		return 0
	}
	return int(cache[maxPseudo])
}

func buildPulseCacheLookup50() pulseCacheLookup50Data {
	var data pulseCacheLookup50Data
	for _, start16 := range cacheIndex50 {
		start := int(start16)
		if start < 0 || start >= len(cacheBits50) || data.valid[start] {
			continue
		}
		data.valid[start] = true
		cache := cacheBits50[start:]
		maxPseudo := int(cache[0])
		if maxPseudo > 0 && maxPseudo < len(cache) {
			data.maxBits[start] = cache[maxPseudo]
		}
		for bitsQ3 := 1; bitsQ3 <= pulseCacheLookupBits; bitsQ3++ {
			data.lut[start][bitsQ3-1] = uint8(bitsToPulsesCachedBinarySearch(cache, bitsQ3))
		}
	}
	return data
}

func cacheBits50Offset(cache []uint8) (int, bool) {
	if len(cache) == 0 {
		return 0, false
	}
	ptr := uintptr(unsafe.Pointer(&cache[0]))
	if ptr < cacheBits50Base || ptr >= cacheBits50End {
		return 0, false
	}
	offset := int(ptr - cacheBits50Base)
	if offset < 0 || offset >= len(cacheBits50) || !pulseCacheLookup50.valid[offset] {
		return 0, false
	}
	return offset, true
}

func bitsToPulsesCachedBinarySearch(cache []uint8, bitsQ3 int) int {
	bitsQ3--
	lo := 0
	hi := int(cache[0])
	for range logMaxPseudo {
		mid := (lo + hi + 1) >> 1
		if int(cache[mid]) >= bitsQ3 {
			hi = mid
		} else {
			lo = mid
		}
	}

	loBits := -1
	if lo > 0 {
		loBits = int(cache[lo])
	}
	if bitsQ3-loBits <= int(cache[hi])-bitsQ3 {
		return lo
	}
	return hi
}

func bitsToPulsesCachedFast(cache []uint8, bitsQ3 int) int {
	if offset, ok := cacheBits50Offset(cache); ok {
		idx := bitsQ3 - 1
		if idx < 0 {
			return 0
		}
		if idx >= pulseCacheLookupBits {
			idx = pulseCacheLookupBits - 1
		}
		return int(pulseCacheLookup50.lut[offset][idx])
	}
	return bitsToPulsesCachedBinarySearch(cache, bitsQ3)
}

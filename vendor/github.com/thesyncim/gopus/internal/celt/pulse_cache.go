package celt

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

const noPulseCacheLookupOffset = -1

// pulseCacheView carries the cache slice and its optional static-table lookup
// offset. Custom mode tables use noPulseCacheLookupOffset and binary search.
type pulseCacheView struct {
	bits         []uint8
	staticOffset int
}

var pulseCacheLookup50 = buildPulseCacheLookup50()

func getPulses(i int) int {
	if i < 8 {
		return i
	}
	return (8 + (i & 7)) << ((i >> 3) - 1)
}

func pulseCacheForBand(band, lm int) (pulseCacheView, bool) {
	if band < 0 || band >= MaxBands {
		return pulseCacheView{}, false
	}
	if lm < -1 {
		return pulseCacheView{}, false
	}
	idx := (lm + 1) * MaxBands
	if idx < 0 || idx >= len(cacheIndex50) {
		return pulseCacheView{}, false
	}
	start := int(cacheIndex50[idx+band])
	if start < 0 || start >= len(cacheBits50) {
		return pulseCacheView{}, false
	}
	cache := cacheBits50[start:]
	if len(cache) == 0 {
		return pulseCacheView{}, false
	}
	maxPseudo := int(cache[0])
	if maxPseudo <= 0 || maxPseudo >= len(cache) {
		return pulseCacheView{}, false
	}
	return pulseCacheView{bits: cache, staticOffset: start}, true
}

func pulseCacheTableOffset(cacheBits []uint8, start int) int {
	if len(cacheBits) == len(cacheBits50) && len(cacheBits) > 0 && &cacheBits[0] == &cacheBits50[0] {
		return start
	}
	return noPulseCacheLookupOffset
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

func bitsToPulsesCached(cache pulseCacheView, bitsQ3 int) int {
	if bitsQ3 <= 0 || len(cache.bits) == 0 {
		return 0
	}
	return bitsToPulsesCachedFast(cache, bitsQ3)
}

func pulsesToBitsCached(cache pulseCacheView, pulses int) int {
	if pulses <= 0 || len(cache.bits) == 0 {
		return 0
	}
	maxPseudo := int(cache.bits[0])
	if pulses > maxPseudo {
		pulses = maxPseudo
	}
	return int(cache.bits[pulses]) + 1
}

func pulseCacheMaxBits(cache pulseCacheView) int {
	if len(cache.bits) == 0 {
		return 0
	}
	maxPseudo := int(cache.bits[0])
	if maxPseudo <= 0 || maxPseudo >= len(cache.bits) {
		return 0
	}
	return int(cache.bits[maxPseudo])
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

func bitsToPulsesCachedFast(cache pulseCacheView, bitsQ3 int) int {
	if offset := cache.staticOffset; offset >= 0 && offset < len(cacheBits50) && pulseCacheLookup50.valid[offset] {
		idx := bitsQ3 - 1
		if idx < 0 {
			return 0
		}
		if idx >= pulseCacheLookupBits {
			idx = pulseCacheLookupBits - 1
		}
		return int(pulseCacheLookup50.lut[offset][idx])
	}
	return bitsToPulsesCachedBinarySearch(cache.bits, bitsQ3)
}

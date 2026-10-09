//go:build !arm64 || nosimd || purego || !goexperiment.simd

package celt

// celtAbsSumUsesNeon selects the four-lane Go SIMD reduction only in the
// arm64 SIMD build. Ordinary and nosimd builds keep the scalar reduction.
const celtAbsSumUsesNeon = false

// l1AbsSumNeon preserves the four-lane reduction order for reference checks.
// The production path in this file remains scalar.
func l1AbsSumNeon(tmp []float32, n int) float32 {
	n = min(n, len(tmp))
	var acc [4]float32
	i := 0
	for ; i+4 <= n; i += 4 {
		for lane := range 4 {
			v := tmp[i+lane]
			if v < 0 {
				v = -v
			}
			acc[lane] += v
		}
	}
	var tail float32
	for ; i < n; i++ {
		v := tmp[i]
		if v < 0 {
			v = -v
		}
		tail += v
	}
	return ((acc[0] + acc[1]) + (acc[2] + acc[3])) + tail
}

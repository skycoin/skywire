//go:build amd64 && !goexperiment.simd && !nosimd && !purego

package silk

import (
	"unsafe"

	"github.com/thesyncim/gopus/internal/opusmath"
)

var silkUsePitchXcorrAVX2FMA = false

// xcorrKernelAVX8 preserves the eight-lane FMA accumulation order used by
// libopus' x86 SILK pitch search.
func xcorrKernelAVX8(x, y *float32, sum *[8]float32, length int) {
	xs := unsafe.Slice(x, length)
	ys := unsafe.Slice(y, length+7)
	var lanes [8][8]float32
	for i := range xs {
		xv := xs[i]
		for corr := range 8 {
			lane := i & 7
			lanes[corr][lane] = opusmath.FMA32(xv, ys[i+corr], lanes[corr][lane])
		}
	}
	for corr := range 8 {
		v := lanes[corr]
		s04 := v[0] + v[4]
		s15 := v[1] + v[5]
		s26 := v[2] + v[6]
		s37 := v[3] + v[7]
		sum[corr] = (s04 + s15) + (s26 + s37)
	}
}

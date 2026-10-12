//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// stereoMergeRescaleNEON applies the final mid/side rescale of stereoMerge over
// len(x) lanes in place (l=mid*x; x=lgain*(l-y); y=rgain*(l+y)). It processes
// eight samples as two NEON vectors, then one vector and a scalar tail. The
// length check validates y before writes, and each array view stays within the
// corresponding slice. Mul, Sub and Add remain distinct operations, preserving
// the scalar noFMA32 reference's rounding order.
func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32) {
	n := len(x)
	if n <= 0 {
		return
	}
	_ = y[:n]
	midv := archsimd.BroadcastFloat32x4(mid)
	lv := archsimd.BroadcastFloat32x4(lgain)
	rv := archsimd.BroadcastFloat32x4(rgain)
	i := 0
	for ; i+8 <= n; i += 8 {
		stereoMergeBlock4((*[4]float32)(x[i:]), (*[4]float32)(y[i:]), midv, lv, rv)
		stereoMergeBlock4((*[4]float32)(x[i+4:]), (*[4]float32)(y[i+4:]), midv, lv, rv)
	}
	for ; i+4 <= n; i += 4 {
		stereoMergeBlock4((*[4]float32)(x[i:]), (*[4]float32)(y[i:]), midv, lv, rv)
	}
	for ; i < n; i++ {
		xv := x[i]
		yv := y[i]
		l := noFMA32Mul(mid, xv)
		x[i] = noFMA32Mul(lgain, noFMA32Sub(l, yv))
		y[i] = noFMA32Mul(rgain, noFMA32Add(l, yv))
	}
}

// stereoMergeBlock4 rescales one 4-lane array view in place.
func stereoMergeBlock4(xBlock, yBlock *[4]float32, midv, lv, rv archsimd.Float32x4) {
	xv := archsimd.LoadFloat32x4Array(xBlock)
	yv := archsimd.LoadFloat32x4Array(yBlock)
	l := midv.Mul(xv)
	lv.Mul(l.Sub(yv)).StoreArray(xBlock)
	rv.Mul(l.Add(yv)).StoreArray(yBlock)
}

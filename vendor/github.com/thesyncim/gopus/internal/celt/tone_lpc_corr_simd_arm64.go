//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// The pinned SIMD arm64 celt_encoder.c:tone_lpc vectorizes across the three
// correlations. Its even prefix rounds each product before an ordered add;
// only the final odd sample uses FMA. Each lane retains ascending sample order.
func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	if cnt == 0 {
		return
	}
	_ = x[delay2+cnt-1]
	acc := archsimd.BroadcastFloat32x4(0)
	i := 0
	for ; i+1 < cnt; i += 2 {
		x0 := x[i]
		y0 := archsimd.BroadcastFloat32x4(x0).SetElem(1, x[i+delay]).SetElem(2, x[i+delay2])
		acc = acc.Add(archsimd.BroadcastFloat32x4(x0).Mul(y0))
		x1 := x[i+1]
		y1 := archsimd.BroadcastFloat32x4(x1).SetElem(1, x[i+1+delay]).SetElem(2, x[i+1+delay2])
		acc = acc.Add(archsimd.BroadcastFloat32x4(x1).Mul(y1))
	}
	if i < cnt {
		xi := x[i]
		y := archsimd.BroadcastFloat32x4(xi).SetElem(1, x[i+delay]).SetElem(2, x[i+delay2])
		acc = archsimd.BroadcastFloat32x4(xi).MulAdd(y, acc)
	}
	return acc.GetElem(0), acc.GetElem(1), acc.GetElem(2)
}

func toneLPCCorrDelay1(x []float32, cnt int) (r00, r01, r02 float32) {
	return toneLPCCorr(x, cnt, 1, 2)
}

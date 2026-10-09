//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

func celtInnerProdSSEStyleImpl(x, y []celtNorm) float32 {
	return celtInnerProdSSEStyleDispatch(x, y, archsimd.X86.AVX())
}

func celtInnerProdSSEStyleDispatch(x, y []celtNorm, avx bool) float32 {
	if !avx {
		return celtInnerProdSSEStyleGo(x, y)
	}
	return celtInnerProdSSEStyleSIMD(x, y)
}

func celtInnerProdSSEStyleSIMD(x, y []celtNorm) float32 {
	n := min(len(x), len(y))
	var acc archsimd.Float32x4
	i := 0
	for ; i+4 <= n; i += 4 {
		vx := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Pointer(&x[i])))
		vy := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Pointer(&y[i])))
		acc = acc.Add(vx.Mul(vy))
	}
	sum0 := round32(acc.GetElem(0) + acc.GetElem(2))
	sum1 := round32(acc.GetElem(1) + acc.GetElem(3))
	sum := round32(sum0 + sum1)
	for ; i < n; i++ {
		sum = celtFloatMulAdd(float32(x[i]), float32(y[i]), sum)
	}
	return sum
}

// celtInnerProdSSEStylePair returns celtInnerProdSSEStyleImpl(x1, y1) and
// celtInnerProdSSEStyleImpl(x2, y2) for equal-length pairs. Each product
// keeps its own celt_inner_prod_sse accumulator, reduction and scalar tail;
// the two accumulator chains are independent, so they share one loop.
func celtInnerProdSSEStylePair(x1, y1, x2, y2 []celtNorm) (float32, float32) {
	n := min(len(x1), len(y1))
	if len(x2) < n || len(y2) < n || !archsimd.X86.AVX() {
		return celtInnerProdSSEStyleImpl(x1, y1), celtInnerProdSSEStyleImpl(x2, y2)
	}
	p1, q1 := unsafe.Pointer(unsafe.SliceData(x1)), unsafe.Pointer(unsafe.SliceData(y1))
	p2, q2 := unsafe.Pointer(unsafe.SliceData(x2)), unsafe.Pointer(unsafe.SliceData(y2))
	var acc1, acc2 archsimd.Float32x4
	i := 0
	for ; i+4 <= n; i += 4 {
		off := uintptr(i) * 4
		acc1 = acc1.Add(loadF32x4(unsafe.Add(p1, off)).Mul(loadF32x4(unsafe.Add(q1, off))))
		acc2 = acc2.Add(loadF32x4(unsafe.Add(p2, off)).Mul(loadF32x4(unsafe.Add(q2, off))))
	}
	s1 := round32(round32(acc1.GetElem(0)+acc1.GetElem(2)) + round32(acc1.GetElem(1)+acc1.GetElem(3)))
	s2 := round32(round32(acc2.GetElem(0)+acc2.GetElem(2)) + round32(acc2.GetElem(1)+acc2.GetElem(3)))
	for ; i < n; i++ {
		s1 = celtFloatMulAdd(float32(x1[i]), float32(y1[i]), s1)
		s2 = celtFloatMulAdd(float32(x2[i]), float32(y2[i]), s2)
	}
	return s1, s2
}

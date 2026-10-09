//go:build (amd64 || arm64) && goexperiment.simd && !nosimd && !purego

package celt

import "unsafe"

// scaleFloat32Into computes dst[i] = src[i]*gain over min(len(dst),len(src))
// elements as 4-wide Float32x4.Mul products: each lane is the same
// single-rounding product as the scalar loop, so the result is bit-exact.
//
// It loads through *[4]float32 views over advancing unsafe pointers instead of
// archsimd.LoadFloat32x4(src[i:]), which would emit a length bounds check and a
// panic path on every load and store of this load/store-bound kernel. Each
// pointer advances only when a later element remains, so no pointer reaches one
// past its slice; an empty slice skips all loops, so SliceData is never
// dereferenced.
func scaleFloat32Into(dst, src []float32, gain float32) {
	if !hasCELTFloat32x4SIMD() {
		scaleFloat32IntoScalar(dst, src, gain)
		return
	}
	scaleFloat32IntoArchSIMD(dst, src, gain)
}

//go:noinline
func scaleFloat32IntoArchSIMD(dst, src []float32, gain float32) {
	n := min(len(dst), len(src))
	g := broadcastF32x4Arch(gain)
	sp := unsafe.Pointer(unsafe.SliceData(src))
	dp := unsafe.Pointer(unsafe.SliceData(dst))
	i := 0
	for ; i+16 <= n; i += 16 {
		storeF32x4(dp, loadF32x4(sp).Mul(g))
		storeF32x4(unsafe.Add(dp, 16), loadF32x4(unsafe.Add(sp, 16)).Mul(g))
		storeF32x4(unsafe.Add(dp, 32), loadF32x4(unsafe.Add(sp, 32)).Mul(g))
		storeF32x4(unsafe.Add(dp, 48), loadF32x4(unsafe.Add(sp, 48)).Mul(g))
		if i+16 < n {
			sp = unsafe.Add(sp, 64)
			dp = unsafe.Add(dp, 64)
		}
	}
	for ; i+4 <= n; i += 4 {
		storeF32x4(dp, loadF32x4(sp).Mul(g))
		if i+4 < n {
			sp = unsafe.Add(sp, 16)
			dp = unsafe.Add(dp, 16)
		}
	}
	for ; i < n; i++ {
		*(*float32)(dp) = noFMA32Mul(*(*float32)(sp), gain)
		if i+1 < n {
			sp = unsafe.Add(sp, 4)
			dp = unsafe.Add(dp, 4)
		}
	}
}

func scaleFloat32IntoScalar(dst, src []float32, gain float32) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = noFMA32Mul(src[i], gain)
	}
}

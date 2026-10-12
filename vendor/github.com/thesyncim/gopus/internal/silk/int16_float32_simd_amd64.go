//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"simd/archsimd"
)

var int16ToFloat32UsesAVX2 = archsimd.X86.AVX2()

// writeInt16AsFloat32Core writes float32(src[i]) / 32768 to dst[i] for i < n,
// eight samples per step on 128-bit vectors. The int16 to float conversion and
// the scale by a power of two are exact, so every lane matches the scalar loop.
func writeInt16AsFloat32Core(dst []float32, src []int16, n int) {
	if n <= 0 {
		return
	}
	dst = dst[:n]
	src = src[:n]
	const inv32768 = 1.0 / 32768.0
	i := 0
	if int16ToFloat32UsesAVX2 {
		i = writeInt16AsFloat32CoreAVX2(dst, src)
	}
	for ; i < n; i++ {
		dst[i] = float32(src[i]) * inv32768
	}
}

//go:noinline
func writeInt16AsFloat32CoreAVX2(dst []float32, src []int16) int {
	const inv32768 = 1.0 / 32768.0
	scale := archsimd.BroadcastFloat32x4(inv32768)
	var zero archsimd.Int16x8
	i := 0
	for ; i <= len(src)-8 && i <= len(dst)-8; i += 8 {
		srcBlock := (*[8]int16)(src[i:])
		v := archsimd.LoadInt16x8Array(srcBlock)
		dstBlock := (*[8]float32)(dst[i:])
		lo := v.ExtendLo4ToInt32()
		// The high four samples, sign-extended through the high half of
		// each lane.
		hi := zero.InterleaveHi(v).AsInt32x4().ShiftAllRight(16)
		lo.ConvertToFloat32().Mul(scale).StoreArray((*[4]float32)(dstBlock[:4]))
		hi.ConvertToFloat32().Mul(scale).StoreArray((*[4]float32)(dstBlock[4:]))
	}
	return i
}

//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

var floatToInt16UsesAVX = archsimd.X86.AVX()

// floatToInt16Scaled converts eight products per step: VROUNDPS rounds to
// nearest with ties to even, VCVTTPS2DQ then converts the integral values (and
// yields the x86 integer-indefinite value for NaN and int32 overflow, as the
// scalar cvtss2si path does), and VPACKSSDW saturates to int16.
func floatToInt16Scaled(out []int16, in []float32, scale float32, n int) {
	out = out[:n]
	in = in[:n]
	i := 0
	if floatToInt16UsesAVX {
		i = floatToInt16ScaledAVX(out, in, scale)
	}
	floatToInt16ScaledScalar(out[i:], in[i:], scale)
}

// floatToInt16ScaledAVX converts the whole groups of eight samples and returns
// how many it consumed. It is a separate call so that scale is spilled by the
// caller before the 256-bit region, which then runs no legacy SSE instruction
// before its closing ClearAVXUpperBits.
//
//go:noinline
func floatToInt16ScaledAVX(out []int16, in []float32, scale float32) int {
	s := archsimd.BroadcastFloat32x8(scale)
	i := 0
	for ; i+8 <= len(in); i += 8 {
		v := archsimd.LoadFloat32x8Array((*[8]float32)(in[i : i+8])).Mul(s).Round().ConvertToInt32()
		v.GetLo().SaturateToInt16Concat(v.GetHi()).StoreArray((*[8]int16)(out[i : i+8]))
	}
	archsimd.ClearAVXUpperBits()
	return i
}

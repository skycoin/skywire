//go:build arm64 && goexperiment.simd && !nosimd && !purego

package gopus

import "simd/archsimd"

func convertFloat32ToInt16Unit(dst []int16, src []float32, n int) bool {
	if n <= 0 {
		return true
	}
	_ = dst[n-1]
	_ = src[n-1]
	blocks := n &^ 15
	if blocks != 0 && !convertFloat32ToInt16UnitBlocks(dst, src, blocks) {
		return false
	}
	for i := blocks; i < n; i++ {
		v := src[i]
		if !(v >= -1 && v <= 1) {
			return false
		}
		dst[i] = float32ToInt16(v)
	}
	return true
}

func convertFloat32ToInt16NoSoftClipUnit(dst []int16, src []float32, n int) {
	if n <= 0 {
		return
	}
	_ = dst[n-1]
	_ = src[n-1]
	blocks := n &^ 15
	if blocks != 0 {
		convertFloat32ToInt16SaturatingBlocks(dst, src, blocks)
	}
	for i := blocks; i < n; i++ {
		dst[i] = float32ToInt16(src[i])
	}
}

func convertFloat32ToInt16UnitBlocks(dst []int16, src []float32, n int) bool {
	if n == 0 {
		return true
	}
	_ = dst[n-1]
	_ = src[n-1]
	one := archsimd.BroadcastFloat32x4(1)
	scale := archsimd.BroadcastFloat32x4(32768)
	for i := 0; i < n; i += 16 {
		srcBlock := (*[16]float32)(src[i:])
		dstBlock := (*[16]int16)(dst[i:])
		v0 := archsimd.LoadFloat32x4Array((*[4]float32)(srcBlock[0:4]))
		v1 := archsimd.LoadFloat32x4Array((*[4]float32)(srcBlock[4:8]))
		v2 := archsimd.LoadFloat32x4Array((*[4]float32)(srcBlock[8:12]))
		v3 := archsimd.LoadFloat32x4Array((*[4]float32)(srcBlock[12:16]))
		valid := v0.Abs().LessEqual(one).
			And(v1.Abs().LessEqual(one)).
			And(v2.Abs().LessEqual(one)).
			And(v3.Abs().LessEqual(one))
		if valid.ToInt32x4().ReduceMax() != -1 {
			return false
		}

		q0 := roundFloat32x4AwayLikeCELT(v0.Mul(scale))
		q1 := roundFloat32x4AwayLikeCELT(v1.Mul(scale))
		q2 := roundFloat32x4AwayLikeCELT(v2.Mul(scale))
		q3 := roundFloat32x4AwayLikeCELT(v3.Mul(scale))
		storeInt16x8((*[8]int16)(dstBlock[0:8]), q0, q1)
		storeInt16x8((*[8]int16)(dstBlock[8:16]), q2, q3)
	}
	return true
}

func convertFloat32ToInt16SaturatingBlocks(dst []int16, src []float32, n int) {
	if n == 0 {
		return
	}
	_ = dst[n-1]
	_ = src[n-1]
	scale := archsimd.BroadcastFloat32x4(32768)
	for i := 0; i < n; i += 16 {
		srcBlock := (*[16]float32)(src[i:])
		dstBlock := (*[16]int16)(dst[i:])
		v0 := archsimd.LoadFloat32x4Array((*[4]float32)(srcBlock[0:4]))
		q0 := roundFloat32x4AwayLikeCELT(v0.Mul(scale))

		v1 := archsimd.LoadFloat32x4Array((*[4]float32)(srcBlock[4:8]))
		q1 := roundFloat32x4AwayLikeCELT(v1.Mul(scale))
		storeInt16x8((*[8]int16)(dstBlock[0:8]), q0, q1)

		v2 := archsimd.LoadFloat32x4Array((*[4]float32)(srcBlock[8:12]))
		q2 := roundFloat32x4AwayLikeCELT(v2.Mul(scale))

		v3 := archsimd.LoadFloat32x4Array((*[4]float32)(srcBlock[12:16]))
		q3 := roundFloat32x4AwayLikeCELT(v3.Mul(scale))
		storeInt16x8((*[8]int16)(dstBlock[8:16]), q2, q3)
	}
}

// celt_float2int16_neon rounds each full 16-sample block with FCVTAS, while
// its scalar remainder uses FLOAT2INT16 (FCVTNS). archsimd.Round ties to even;
// an exact half tie rounded toward zero needs one step away from zero to match
// the vector body in celt/arm/celt_neon_intr.c.
func roundFloat32x4AwayLikeCELT(x archsimd.Float32x4) archsimd.Int32x4 {
	rounded := x.Round()
	towardZeroTie := x.Abs().Sub(rounded.Abs()).Equal(archsimd.BroadcastFloat32x4(0.5)).ToInt32x4()
	sign := x.Less(archsimd.BroadcastFloat32x4(0)).ToInt32x4().Or(archsimd.BroadcastInt32x4(1))
	return rounded.ConvertToInt32().Add(towardZeroTie.And(sign))
}

func storeInt16x8(dst *[8]int16, lo, hi archsimd.Int32x4) {
	lo16 := lo.SaturateToInt16().ToBits().ReshapeToUint64s()
	hi16 := hi.SaturateToInt16().ToBits().ReshapeToUint64s()
	lo16.ConcatEven(hi16).ReshapeToUint16s().BitsToInt16().StoreArray(dst)
}

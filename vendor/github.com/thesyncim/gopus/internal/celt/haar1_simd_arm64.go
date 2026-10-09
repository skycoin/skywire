//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

const haarScale = float32(0.7071067811865476)

func haar1Stride1(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	_ = x[2*n0-1]
	scale := archsimd.BroadcastFloat32x4(haarScale)
	i := 0
	for ; i+4 <= n0; i += 4 {
		chunk := (*[8]float32)(x[2*i : 2*i+8])
		lo := (*[4]float32)(chunk[:4])
		hi := (*[4]float32)(chunk[4:])
		a := archsimd.LoadFloat32x4Array(lo).ToBits()
		b := archsimd.LoadFloat32x4Array(hi).ToBits()
		even := a.ConcatEven(b).BitsToFloat32()
		odd := a.ConcatOdd(b).BitsToFloat32()
		// haar1 scales each input before the butterfly (tmp1 = c*a, tmp2 = c*b).
		sum := even.Mul(scale).Add(odd.Mul(scale)).ToBits()
		diff := even.Mul(scale).Sub(odd.Mul(scale)).ToBits()
		sum.InterleaveLo(diff).BitsToFloat32().StoreArray(lo)
		sum.InterleaveHi(diff).BitsToFloat32().StoreArray(hi)
	}
	for ; i < n0; i++ {
		a, b := x[2*i], x[2*i+1]
		x[2*i] = noFMA32Mul(haarScale, a) + noFMA32Mul(haarScale, b)
		x[2*i+1] = noFMA32Mul(haarScale, a) - noFMA32Mul(haarScale, b)
	}
}

func haar1Stride2(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	_ = x[4*n0-1]
	scale := archsimd.BroadcastFloat32x4(haarScale)
	i := 0
	for ; i+2 <= n0; i += 2 {
		chunk := (*[8]float32)(x[4*i : 4*i+8])
		loView := (*[4]float32)(chunk[:4])
		hiView := (*[4]float32)(chunk[4:])
		a := archsimd.LoadFloat32x4Array(loView).ToBits().ReshapeToUint64s()
		b := archsimd.LoadFloat32x4Array(hiView).ToBits().ReshapeToUint64s()
		lo := a.InterleaveLo(b).ReshapeToUint32s().BitsToFloat32()
		hi := a.InterleaveHi(b).ReshapeToUint32s().BitsToFloat32()
		sum := lo.Mul(scale).Add(hi.Mul(scale)).ToBits()
		diff := lo.Mul(scale).Sub(hi.Mul(scale)).ToBits()
		sum64 := sum.ReshapeToUint64s()
		diff64 := diff.ReshapeToUint64s()
		sum64.InterleaveLo(diff64).ReshapeToUint32s().BitsToFloat32().StoreArray(loView)
		sum64.InterleaveHi(diff64).ReshapeToUint32s().BitsToFloat32().StoreArray(hiView)
	}
	for ; i < n0; i++ {
		off := 4 * i
		a, b, c, d := x[off], x[off+1], x[off+2], x[off+3]
		x[off] = noFMA32Mul(haarScale, a) + noFMA32Mul(haarScale, c)
		x[off+1] = noFMA32Mul(haarScale, b) + noFMA32Mul(haarScale, d)
		x[off+2] = noFMA32Mul(haarScale, a) - noFMA32Mul(haarScale, c)
		x[off+3] = noFMA32Mul(haarScale, b) - noFMA32Mul(haarScale, d)
	}
}

func haar1Stride4(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	_ = x[8*n0-1]
	scale := archsimd.BroadcastFloat32x4(haarScale)
	i := 0
	for ; i+2 <= n0; i += 2 {
		chunk := (*[16]float32)(x[8*i : 8*i+16])
		lo0 := (*[4]float32)(chunk[:4])
		hi0 := (*[4]float32)(chunk[4:8])
		lo1 := (*[4]float32)(chunk[8:12])
		hi1 := (*[4]float32)(chunk[12:])
		lo0Vec := archsimd.LoadFloat32x4Array(lo0)
		hi0Vec := archsimd.LoadFloat32x4Array(hi0)
		lo1Vec := archsimd.LoadFloat32x4Array(lo1)
		hi1Vec := archsimd.LoadFloat32x4Array(hi1)
		scaledLo0 := lo0Vec.Mul(scale)
		scaledHi0 := hi0Vec.Mul(scale)
		scaledLo1 := lo1Vec.Mul(scale)
		scaledHi1 := hi1Vec.Mul(scale)
		scaledLo0.Add(scaledHi0).StoreArray(lo0)
		scaledLo0.Sub(scaledHi0).StoreArray(hi0)
		scaledLo1.Add(scaledHi1).StoreArray(lo1)
		scaledLo1.Sub(scaledHi1).StoreArray(hi1)
	}
	if i < n0 {
		chunk := (*[8]float32)(x[8*i : 8*i+8])
		lo := (*[4]float32)(chunk[:4])
		hi := (*[4]float32)(chunk[4:])
		loVec := archsimd.LoadFloat32x4Array(lo)
		hiVec := archsimd.LoadFloat32x4Array(hi)
		scaledLo := loVec.Mul(scale)
		scaledHi := hiVec.Mul(scale)
		scaledLo.Add(scaledHi).StoreArray(lo)
		scaledLo.Sub(scaledHi).StoreArray(hi)
	}
}

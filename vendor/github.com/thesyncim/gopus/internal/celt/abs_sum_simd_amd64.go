//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// absSumMask clears the float32 sign bit, as fabsf does.
var absSumMask = [4]uint32{0x7fffffff, 0x7fffffff, 0x7fffffff, 0x7fffffff}

// absSumQuad returns absSumSerial(a), absSumSerial(b), absSumSerial(c) and
// absSumSerial(d); b, c and d hold at least len(a) elements. One vector lane
// carries each slice's accumulator: every four-element block is transposed so
// that element i of all four slices lands in one vector, and the adds run in
// element order, so each lane performs exactly its slice's serial additions.
func absSumQuad(a, b, c, d []float32) (float32, float32, float32, float32) {
	if !archsimd.X86.AVX() {
		return absSumSerial4(a, b, c, d)
	}
	return absSumQuadAVX(a, b, c, d)
}

//go:noinline
func absSumQuadAVX(a, b, c, d []float32) (float32, float32, float32, float32) {
	n := len(a)
	b, c, d = b[:n], c[:n], d[:n]
	blocks := n &^ 3
	var s0, s1, s2, s3 float32
	if blocks > 0 {
		mask := archsimd.LoadUint32x4Array(&absSumMask)
		pa := unsafe.Pointer(unsafe.SliceData(a))
		pb := unsafe.Pointer(unsafe.SliceData(b))
		pc := unsafe.Pointer(unsafe.SliceData(c))
		pd := unsafe.Pointer(unsafe.SliceData(d))
		var acc archsimd.Float32x4
		for j := 0; j < blocks; j += 4 {
			off := uintptr(j) * 4
			va := loadF32x4(unsafe.Add(pa, off)).ToBits().And(mask)
			vb := loadF32x4(unsafe.Add(pb, off)).ToBits().And(mask)
			vc := loadF32x4(unsafe.Add(pc, off)).ToBits().And(mask)
			vd := loadF32x4(unsafe.Add(pd, off)).ToBits().And(mask)
			ab01 := va.InterleaveLo(vb).BitsToFloat32() // a0 b0 a1 b1
			cd01 := vc.InterleaveLo(vd).BitsToFloat32() // c0 d0 c1 d1
			ab23 := va.InterleaveHi(vb).BitsToFloat32() // a2 b2 a3 b3
			cd23 := vc.InterleaveHi(vd).BitsToFloat32() // c2 d2 c3 d3
			acc = acc.Add(ab01.ConcatPermuteScalars(0, 1, 4, 5, cd01))
			acc = acc.Add(ab01.ConcatPermuteScalars(2, 3, 6, 7, cd01))
			acc = acc.Add(ab23.ConcatPermuteScalars(0, 1, 4, 5, cd23))
			acc = acc.Add(ab23.ConcatPermuteScalars(2, 3, 6, 7, cd23))
		}
		s0, s1, s2, s3 = acc.GetElem(0), acc.GetElem(1), acc.GetElem(2), acc.GetElem(3)
	}
	for j := blocks; j < n; j++ {
		s0 += absF32(a[j])
		s1 += absF32(b[j])
		s2 += absF32(c[j])
		s3 += absF32(d[j])
	}
	return s0, s1, s2, s3
}

// absSumPair is absSumQuad for two slices: the two accumulators occupy the
// low lanes, and each block's elements are added in order.
func absSumPair(a, b []float32) (float32, float32) {
	if !archsimd.X86.AVX() {
		return absSumSerial2(a, b)
	}
	return absSumPairAVX(a, b)
}

//go:noinline
func absSumPairAVX(a, b []float32) (float32, float32) {
	n := len(a)
	b = b[:n]
	blocks := n &^ 3
	var s0, s1 float32
	if blocks > 0 {
		mask := archsimd.LoadUint32x4Array(&absSumMask)
		pa := unsafe.Pointer(unsafe.SliceData(a))
		pb := unsafe.Pointer(unsafe.SliceData(b))
		var acc archsimd.Float32x4
		for j := 0; j < blocks; j += 4 {
			off := uintptr(j) * 4
			va := loadF32x4(unsafe.Add(pa, off)).ToBits().And(mask)
			vb := loadF32x4(unsafe.Add(pb, off)).ToBits().And(mask)
			ab01 := va.InterleaveLo(vb).BitsToFloat32() // a0 b0 a1 b1
			ab23 := va.InterleaveHi(vb).BitsToFloat32() // a2 b2 a3 b3
			acc = acc.Add(ab01)
			acc = acc.Add(ab01.ConcatPermuteScalars(2, 3, 2, 3, ab01))
			acc = acc.Add(ab23)
			acc = acc.Add(ab23.ConcatPermuteScalars(2, 3, 2, 3, ab23))
		}
		s0, s1 = acc.GetElem(0), acc.GetElem(1)
	}
	for j := blocks; j < n; j++ {
		s0 += absF32(a[j])
		s1 += absF32(b[j])
	}
	return s0, s1
}

// absSumFive is absSumQuad for the first four slices and absSumSerial for e.
func absSumFive(a, b, c, d, e []float32) (float32, float32, float32, float32, float32) {
	s0, s1, s2, s3 := absSumQuad(a, b, c, d)
	return s0, s1, s2, s3, absSumSerial(e[:len(a)])
}

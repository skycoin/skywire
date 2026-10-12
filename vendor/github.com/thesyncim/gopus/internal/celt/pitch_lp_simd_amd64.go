//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"unsafe"

	"simd/archsimd"
)

var celtFIR5UsesAVX = archsimd.X86.AVX()

// celtFIR5F32 is libopus celt_fir5() applied in place to x. The filter has no
// feedback: every output is x[i] plus the five taps on the original inputs
// x[i-1] ... x[i-5]. The AVX path computes four outputs per vector in the
// selected libopus MAC order and walks from the end of x, so the inputs a
// vector reads are not yet overwritten.
func celtFIR5F32(x []float32, num [5]float32) {
	if !celtFIR5UsesAVX || (len(x) < 9 && !celtFIR5UsesFMA) {
		celtFIR5Scalar(x, num)
		return
	}
	celtFIR5AVX(x, num)
}

//go:noinline
func celtFIR5AVX(x []float32, num [5]float32) {
	p := unsafe.Pointer(unsafe.SliceData(x))
	n0 := broadcastF32x4Arch(num[0])
	n1 := broadcastF32x4Arch(num[1])
	n2 := broadcastF32x4Arch(num[2])
	n3 := broadcastF32x4Arch(num[3])
	n4 := broadcastF32x4Arch(num[4])
	// Vector i covers outputs [i, i+4) and reads x[i-5 : i+4], so i >= 5.
	i := len(x) - 4
	for ; i >= 5; i -= 4 {
		off := unsafe.Add(p, i*4)
		s := loadF32x4(off)
		s = celtFIR5Accumulate(s, n0, loadF32x4(unsafe.Add(off, -4)))
		s = celtFIR5Accumulate(s, n1, loadF32x4(unsafe.Add(off, -8)))
		s = celtFIR5Accumulate(s, n2, loadF32x4(unsafe.Add(off, -12)))
		s = celtFIR5Accumulate(s, n3, loadF32x4(unsafe.Add(off, -16)))
		s = celtFIR5Accumulate(s, n4, loadF32x4(unsafe.Add(off, -20)))
		storeF32x4(off, s)
	}
	celtFIR5Head(x[:i+4], num)
}

// pitchDownsample2 is the factor-2 pitch_downsample() decimation of outputs
// [1, len(dst)); see pitchDownsample2Scalar. The AVX path produces four
// outputs per step and leaves the tail to the scalar loop.
func pitchDownsample2(dst, x0, x1 []float32) {
	start := 1
	if celtFIR5UsesAVX && len(dst) > 5 {
		start = pitchDownsample2AVX(dst, x0, x1)
	}
	pitchDownsample2Scalar(dst, x0, x1, start)
}

// pitchDownsample2AVX computes outputs from 1 upward four at a time while
// i+4 < len(dst) and returns the first output it leaves to the scalar loop.
// Outputs i..i+3 read x[2i-1 : 2i+9], which stays inside the 2*len(dst)
// inputs. With a = x[2i-1:2i+3] and b = x[2i+3:2i+7], the left taps
// x[2i-1+2k] are lanes (a0, a2, b0, b2) and the center taps x[2i+2k] are
// (a1, a3, b1, b3); the right taps come the same way from the loads one
// sample later. Each lane sums its three products in the scalar order.
//
//go:noinline
func pitchDownsample2AVX(dst, x0, x1 []float32) int {
	n := len(dst)
	_ = x0[2*n-1]
	q := broadcastF32x4Arch(0.25)
	h := broadcastF32x4Arch(0.5)
	pd := unsafe.Pointer(unsafe.SliceData(dst))
	p0 := unsafe.Pointer(unsafe.SliceData(x0))
	decimate := func(off unsafe.Pointer) archsimd.Float32x4 {
		a := loadF32x4(off)
		b := loadF32x4(unsafe.Add(off, 16))
		ar := loadF32x4(unsafe.Add(off, 8))
		br := loadF32x4(unsafe.Add(off, 24))
		left := a.ConcatPermuteScalars(0, 2, 4, 6, b)
		center := a.ConcatPermuteScalars(1, 3, 5, 7, b)
		right := ar.ConcatPermuteScalars(0, 2, 4, 6, br)
		return q.Mul(left).Add(q.Mul(right)).Add(h.Mul(center))
	}
	i := 1
	if x1 == nil {
		for ; i+4 < n; i += 4 {
			storeF32x4(unsafe.Add(pd, i*4), decimate(unsafe.Add(p0, (2*i-1)*4)))
		}
		return i
	}
	_ = x1[2*n-1]
	p1 := unsafe.Pointer(unsafe.SliceData(x1))
	for ; i+4 < n; i += 4 {
		v0 := decimate(unsafe.Add(p0, (2*i-1)*4))
		v1 := decimate(unsafe.Add(p1, (2*i-1)*4))
		storeF32x4(unsafe.Add(pd, i*4), v0.Add(v1))
	}
	return i
}

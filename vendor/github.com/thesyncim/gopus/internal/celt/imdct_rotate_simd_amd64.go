//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// The x86 libopus SIMD pre-rotation uses separate multiplies and add/subtract.
// The post-rotation uses separate products through AMD64 v2 and fuses the
// first product on AMD64 v3, so the lane kernels follow that target shape.

func evenLanes4(lo, hi archsimd.Float32x4) archsimd.Float32x4 {
	return lo.ConcatPermuteScalars(0, 2, 4, 6, hi)
}

func oddLanes4(lo, hi archsimd.Float32x4) archsimd.Float32x4 {
	return lo.ConcatPermuteScalars(1, 3, 5, 7, hi)
}

func reverseLanes4(v archsimd.Float32x4) archsimd.Float32x4 {
	return v.ConcatPermuteScalars(3, 2, 5, 4, v)
}

func storeInterleaved4(p unsafe.Pointer, even, odd archsimd.Float32x4) {
	e, o := even.ToBits(), odd.ToBits()
	storeF32x4(p, e.InterleaveLo(o).BitsToFloat32())
	storeF32x4(unsafe.Add(p, 16), e.InterleaveHi(o).BitsToFloat32())
}

// imdctPostRotateF32FromKiss computes, per iteration i (k = n4-1-i), with
// re=fft[i].i, im=fft[i].r, re2=fft[k].i, im2=fft[k].r:
//
//	buf[2i]      = re*trig[i]       + im*trig[n4+i]
//	buf[n2-1-2i] = re*trig[n4+i]    - im*trig[i]
//	buf[n2-2-2i] = re2*trig[n4-1-i] + im2*trig[n2-1-i]
//	buf[2i+1]    = re2*trig[n2-1-i] - im2*trig[n4-1-i]
//
// four iterations per block, then a scalar loop for limit%4.
func imdctPostRotateF32FromKiss(buf []float32, fft []kissCpx, trig []float32, n2, n4 int) {
	if !archsimd.X86.AVX() {
		imdctPostRotateF32FromKissScalar(buf, fft, trig, n2, n4)
		return
	}
	if len(buf) < n2 || len(fft) < n4 {
		return
	}
	limit := (n4 + 1) >> 1
	if limit <= 0 {
		return
	}
	_ = buf[n2-1]
	_ = fft[n4-1]
	_ = trig[n2-1]

	bp := unsafe.Pointer(unsafe.SliceData(buf))
	ffp := unsafe.Pointer(unsafe.SliceData(fft))
	tp := unsafe.Pointer(unsafe.SliceData(trig))

	i := 0
	for ; i+4 <= limit; i += 4 {
		f0 := loadF32x4(unsafe.Add(ffp, (2*i)*4))
		f1 := loadF32x4(unsafe.Add(ffp, (2*i+4)*4))
		im, re := evenLanes4(f0, f1), oddLanes4(f0, f1)
		t0 := loadF32x4(unsafe.Add(tp, i*4))
		t1 := loadF32x4(unsafe.Add(tp, (n4+i)*4))
		yr := re.Mul(t0).Add(im.Mul(t1))
		yi := re.Mul(t1).Sub(im.Mul(t0))
		if mdctUseFMALikeMixEnabled {
			// The libopus SIMD post-rotation fuses the first source product
			// and rounds the second product before the add/subtract.
			yr = re.MulAdd(t0, im.Mul(t1))
			yi = re.MulAdd(t1, im.Mul(t0).Neg())
		}

		kbase := n4 - 4 - i
		g0 := loadF32x4(unsafe.Add(ffp, (2*kbase)*4))
		g1 := loadF32x4(unsafe.Add(ffp, (2*kbase+4)*4))
		im2 := reverseLanes4(evenLanes4(g0, g1))
		re2 := reverseLanes4(oddLanes4(g0, g1))
		t0b := reverseLanes4(loadF32x4(unsafe.Add(tp, (n4-4-i)*4)))
		t1b := reverseLanes4(loadF32x4(unsafe.Add(tp, (n2-4-i)*4)))
		yr2 := re2.Mul(t0b).Add(im2.Mul(t1b))
		yi2 := re2.Mul(t1b).Sub(im2.Mul(t0b))
		if mdctUseFMALikeMixEnabled {
			yr2 = re2.MulAdd(t0b, im2.Mul(t1b))
			yi2 = re2.MulAdd(t1b, im2.Mul(t0b).Neg())
		}

		storeInterleaved4(unsafe.Add(bp, (2*i)*4), yr, yi2)
		storeInterleaved4(unsafe.Add(bp, (n2-8-2*i)*4), reverseLanes4(yr2), reverseLanes4(yi))
	}
	for ; i < limit; i++ {
		k := n4 - 1 - i
		re, im := fft[i].i, fft[i].r
		t0, t1 := trig[i], trig[n4+i]
		buf[2*i] = mdctMulAddMix(re, im, t0, t1)
		buf[n2-1-2*i] = mdctMulSubMix(re, im, t1, t0)
		re2, im2 := fft[k].i, fft[k].r
		t0, t1 = trig[n4-i-1], trig[n2-i-1]
		buf[n2-2-2*i] = mdctMulAddMix(re2, im2, t0, t1)
		buf[2*i+1] = mdctMulSubMix(re2, im2, t1, t0)
	}
}

// imdctPreRotateNoFMA computes fftIn[i] = (x1*t0 - x2*t1, x2*t0 + x1*t1) with
// x1 = spectrum[2i], x2 = spectrum[n2-1-2i], t0 = trig[i], t1 = trig[n4+i],
// four i per block, then a scalar loop for n4%4.
func imdctPreRotateNoFMA(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	if !archsimd.X86.AVX() {
		imdctPreRotateNoFMAScalar(fftIn, spectrum, trig, n2, n4)
		return
	}
	_ = spectrum[n2-1]
	_ = trig[n2-1]
	_ = fftIn[n4-1]
	_ = spectrum[2*n4-1]

	sp := unsafe.Pointer(unsafe.SliceData(spectrum))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	op := unsafe.Pointer(unsafe.SliceData(fftIn))

	i := 0
	for ; i+4 <= n4; i += 4 {
		s0 := loadF32x4(unsafe.Add(sp, (2*i)*4))
		s1 := loadF32x4(unsafe.Add(sp, (2*i+4)*4))
		x1 := evenLanes4(s0, s1)
		b := n2 - 8 - 2*i
		g0 := loadF32x4(unsafe.Add(sp, b*4))
		g1 := loadF32x4(unsafe.Add(sp, (b+4)*4))
		x2 := reverseLanes4(oddLanes4(g0, g1))
		t0 := loadF32x4(unsafe.Add(tp, i*4))
		t1 := loadF32x4(unsafe.Add(tp, (n4+i)*4))
		re := x1.Mul(t0).Sub(x2.Mul(t1))
		im := x2.Mul(t0).Add(x1.Mul(t1))
		storeInterleaved4(unsafe.Add(op, (2*i)*4), re, im)
	}
	for ; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		fftIn[i] = complex(
			noFMA32Sub(noFMA32Mul(x1, t0), noFMA32Mul(x2, t1)),
			noFMA32Add(noFMA32Mul(x2, t0), noFMA32Mul(x1, t1)),
		)
	}
}

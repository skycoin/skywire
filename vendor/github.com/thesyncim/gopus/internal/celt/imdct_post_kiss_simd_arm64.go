//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "unsafe"

// imdctPostRotateF32FromKiss is the archsimd IMDCT post-rotation, reading kissCpx
// scratch directly. Per iteration i (k = n4-1-i), with re=fft[i].i, im=fft[i].r and
// re2=fft[k].i, im2=fft[k].r:
//
//	buf[2i]      = re*trig[i]      + round(im*trig[n4+i])
//	buf[n2-1-2i] = re*trig[n4+i]   - round(im*trig[i])
//	buf[n2-2-2i] = re2*trig[n4-1-i] + round(im2*trig[n2-1-i])
//	buf[2i+1]    = re2*trig[n2-1-i] - round(im2*trig[n4-1-i])
//
// The forward four come from a ConcatEven/Odd deinterleave of fft; the backward
// four walk down from fft[n4-1] and reverse4 so the descending lanes line up.
// Each product is a single-round Mul and each accumulate a fused MulAdd, matching
// mdctMulAddMix/mdctMulSubMix bit-for-bit; a scalar loop finishes limit%4.
func imdctPostRotateF32FromKiss(buf []float32, fft []kissCpx, trig []float32, n2, n4 int) {
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
		// Forward fft[i..i+3]: deinterleave im=even(.r), re=odd(.i).
		f0 := loadF32x4(unsafe.Add(ffp, (2*i)*4))
		f1 := loadF32x4(unsafe.Add(ffp, (2*i+4)*4))
		im := f0.ToBits().ConcatEven(f1.ToBits()).BitsToFloat32()
		re := f0.ToBits().ConcatOdd(f1.ToBits()).BitsToFloat32()
		t0 := loadF32x4(unsafe.Add(tp, i*4))
		t1 := loadF32x4(unsafe.Add(tp, (n4+i)*4))
		yr := re.MulAdd(t0, im.Mul(t1))
		yi := re.MulAdd(t1, im.Mul(t0).Neg())

		// Backward fft[n4-4-i..n4-1-i]: deinterleave then reverse4 to descend.
		kbase := n4 - 4 - i
		g0 := loadF32x4(unsafe.Add(ffp, (2*kbase)*4))
		g1 := loadF32x4(unsafe.Add(ffp, (2*kbase+4)*4))
		im2 := reverse4(g0.ToBits().ConcatEven(g1.ToBits()).BitsToFloat32())
		re2 := reverse4(g0.ToBits().ConcatOdd(g1.ToBits()).BitsToFloat32())
		t0b := reverse4(loadF32x4(unsafe.Add(tp, (n4-4-i)*4)))
		t1b := reverse4(loadF32x4(unsafe.Add(tp, (n2-4-i)*4)))
		yr2 := re2.MulAdd(t0b, im2.Mul(t1b))
		yi2 := re2.MulAdd(t1b, im2.Mul(t0b).Neg())

		// Near pairs ascending: (buf[2i], buf[2i+1]) = (yr, yi2).
		storeF32x4(unsafe.Add(bp, (2*i)*4), yr.ToBits().InterleaveLo(yi2.ToBits()).BitsToFloat32())
		storeF32x4(unsafe.Add(bp, (2*i+4)*4), yr.ToBits().InterleaveHi(yi2.ToBits()).BitsToFloat32())

		// Far pairs descending: (buf[n2-2-2i], buf[n2-1-2i]) = (yr2, yi), reversed
		// so the four pairs land in memory order at the descending base.
		rYr2 := reverse4(yr2)
		rYi := reverse4(yi)
		fb := n2 - 8 - 2*i
		storeF32x4(unsafe.Add(bp, fb*4), rYr2.ToBits().InterleaveLo(rYi.ToBits()).BitsToFloat32())
		storeF32x4(unsafe.Add(bp, (fb+4)*4), rYr2.ToBits().InterleaveHi(rYi.ToBits()).BitsToFloat32())
	}
	for ; i < limit; i++ {
		k := n4 - 1 - i
		re := fft[i].i
		im := fft[i].r
		t0 := trig[i]
		t1 := trig[n4+i]
		buf[2*i] = mdctMulAddMix(re, im, t0, t1)
		buf[n2-1-2*i] = mdctMulSubMix(re, im, t1, t0)
		re2 := fft[k].i
		im2 := fft[k].r
		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		buf[n2-2-2*i] = mdctMulAddMix(re2, im2, t0, t1)
		buf[2*i+1] = mdctMulSubMix(re2, im2, t1, t0)
	}
}

// imdctPreRotateNoFMA is unreachable on arm64, whose float build takes the
// fused pre-rotation, and stays scalar.
func imdctPreRotateNoFMA(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	imdctPreRotateNoFMAScalar(fftIn, spectrum, trig, n2, n4)
}

//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "unsafe"

// mdctUsePostTwiddleNeon enables the archsimd forward-MDCT post-twiddle.
const mdctUsePostTwiddleNeon = true

// mdctPostTwiddleNeon is the archsimd forward-MDCT post-twiddle. Per index i
// (re=fftStage[i].r, im=fftStage[i].i, t0=trig[i], t1=trig[n4+i]):
//
//	coeffs[2i]      = fma(im, t1, -round(re*t0))
//	coeffs[n2-1-2i] = fma(re, t1, round(im*t0))
//
// Each block pairs a forward run i and its mirror j=n4-1-i so the two ends tile
// coeffs contiguously: the low write zips the forward yr with the reversed
// mirror yi, the high write zips the mirror yr with the reversed forward yi.
// The first product fuses into the combine as clang -ffp-contract=on does for
// clt_mdct_forward_c(), matching mdctMulSubMixEncode/mdctMulAddMixEncode and
// the scalar loop bit-for-bit. The caller does the n4%8
// middle scalarly, so this runs exactly pairBlocks blocks.
func mdctPostTwiddleNeon(coeffs []float32, fftStage []kissCpx, trig []float32, n2, n4, pairBlocks int) {
	if pairBlocks == 0 {
		return
	}
	_ = coeffs[n2-1]
	_ = trig[n2-1]
	_ = fftStage[n4-1]
	cp := unsafe.Pointer(unsafe.SliceData(coeffs))
	ffp := unsafe.Pointer(unsafe.SliceData(fftStage))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	forwardFFT := ffp
	// The final decrement remains within fftStage for pairBlocks <= n4/8,
	// which is the production contract for the SIMD region.
	mirrorFFT := unsafe.Add(ffp, (n4-4)*8)
	forwardT0 := tp
	forwardT1 := unsafe.Add(tp, n4*4)
	mirrorT0 := unsafe.Add(tp, (n4-4)*4)
	mirrorT1 := unsafe.Add(tp, (2*n4-4)*4)
	low := cp
	high := unsafe.Add(cp, (n2-8)*4)
	for b := 0; b < pairBlocks; b++ {
		// Forward fftStage[i..i+3]: re=even(.r), im=odd(.i).
		f0 := loadF32x4(forwardFFT)
		f1 := loadF32x4(unsafe.Add(forwardFFT, 16))
		re := f0.ToBits().ConcatEven(f1.ToBits()).BitsToFloat32()
		im := f0.ToBits().ConcatOdd(f1.ToBits()).BitsToFloat32()
		t0 := loadF32x4(forwardT0)
		t1 := loadF32x4(forwardT1)
		yrF := im.MulAdd(t1, re.Mul(t0).Neg())
		yiF := re.MulAdd(t1, im.Mul(t0))

		// Mirror fftStage[n4-4-i..n4-1-i], ascending j.
		g0 := loadF32x4(mirrorFFT)
		g1 := loadF32x4(unsafe.Add(mirrorFFT, 16))
		reM := g0.ToBits().ConcatEven(g1.ToBits()).BitsToFloat32()
		imM := g0.ToBits().ConcatOdd(g1.ToBits()).BitsToFloat32()
		t0M := loadF32x4(mirrorT0)
		t1M := loadF32x4(mirrorT1)
		yrM := imM.MulAdd(t1M, reM.Mul(t0M).Neg())
		yiM := reM.MulAdd(t1M, imM.Mul(t0M))

		// Low region coeffs[2i..2i+7] = zip(yrF, reverse4(yiM)).
		rYiM := reverse4(yiM)
		storeF32x4(low, yrF.ToBits().InterleaveLo(rYiM.ToBits()).BitsToFloat32())
		storeF32x4(unsafe.Add(low, 16), yrF.ToBits().InterleaveHi(rYiM.ToBits()).BitsToFloat32())

		// High region coeffs[n2-8-2i..n2-1-2i] = zip(yrM, reverse4(yiF)).
		rYiF := reverse4(yiF)
		storeF32x4(high, yrM.ToBits().InterleaveLo(rYiF.ToBits()).BitsToFloat32())
		storeF32x4(unsafe.Add(high, 16), yrM.ToBits().InterleaveHi(rYiF.ToBits()).BitsToFloat32())

		forwardFFT = unsafe.Add(forwardFFT, 32)
		mirrorFFT = unsafe.Add(mirrorFFT, -32)
		forwardT0 = unsafe.Add(forwardT0, 16)
		forwardT1 = unsafe.Add(forwardT1, 16)
		mirrorT0 = unsafe.Add(mirrorT0, -16)
		mirrorT1 = unsafe.Add(mirrorT1, -16)
		low = unsafe.Add(low, 32)
		high = unsafe.Add(high, -32)
	}
}

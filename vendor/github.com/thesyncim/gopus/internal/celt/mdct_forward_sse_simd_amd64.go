//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"unsafe"

	"simd/archsimd"
)

// mdctUseSSEForward runs the bulk of the forward MDCT pre- and post-rotation
// on four float32 lanes when the host supports AVX. The vector helpers remain
// behind noinline calls so unsupported instructions cannot cross this gate.
var mdctUseSSEForward = archsimd.X86.AVX()

// mdctMidRotateSSE is clt_mdct_forward_c's unwindowed fold plus pre-rotation
// for 4*blocks outputs starting at i0: re = in[xp2-2k], im = in[xp1+2k],
// yr = re*t0 - im*t1, yi = im*t0 + re*t1, each scaled and stored at
// fftStage[bitrev[i0+k]]. The loads read in[xp1 : xp1+8*blocks] and
// in[xp2-8*blocks+1 : xp2+1].
//
//go:noinline
func mdctMidRotateSSE(fftStage []kissCpx, bitrev []int, in, trig []float32, i0, n4, xp1, xp2, blocks int, scale float32) {
	_ = in[xp1+8*blocks-1]
	_ = in[xp2]
	_ = in[xp2-8*blocks+1]
	_ = trig[n4+i0+4*blocks-1]
	br := bitrev[i0 : i0+4*blocks]
	_ = fftStage[len(fftStage)-1]
	ip := unsafe.Pointer(unsafe.SliceData(in))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	sp := unsafe.Pointer(unsafe.SliceData(fftStage))
	n := len(fftStage)
	s4 := broadcastF32x4Arch(scale)
	for b := range blocks {
		i := i0 + 4*b
		p1 := unsafe.Add(ip, 4*(xp1+8*b))
		lo1 := loadF32x4(p1)
		hi1 := loadF32x4(unsafe.Add(p1, 16))
		im := lo1.ConcatPermuteScalars(0, 2, 4, 6, hi1)
		p2 := unsafe.Add(ip, 4*(xp2-8*b-7))
		lo2 := loadF32x4(p2)
		hi2 := loadF32x4(unsafe.Add(p2, 16))
		re := hi2.ConcatPermuteScalars(3, 1, 7, 5, lo2)
		t0 := loadF32x4(unsafe.Add(tp, 4*i))
		t1 := loadF32x4(unsafe.Add(tp, 4*(n4+i)))
		yr := re.Mul(t0).Sub(im.Mul(t1)).Mul(s4)
		yi := im.Mul(t0).Add(re.Mul(t1)).Mul(s4)
		pairs01 := yr.ToBits().InterleaveLo(yi.ToBits()).ReshapeToUint64s()
		pairs23 := yr.ToBits().InterleaveHi(yi.ToBits()).ReshapeToUint64s()
		k := br[4*b : 4*b+4]
		mdctStorePair(sp, n, k[0], pairs01.GetElem(0))
		mdctStorePair(sp, n, k[1], pairs01.GetElem(1))
		mdctStorePair(sp, n, k[2], pairs23.GetElem(0))
		mdctStorePair(sp, n, k[3], pairs23.GetElem(1))
	}
}

// mdctStorePair writes one packed {r, i} pair to fftStage[idx].
func mdctStorePair(sp unsafe.Pointer, n, idx int, v uint64) {
	if uint(idx) >= uint(n) {
		panic("mdct: bit-reversed index out of range")
	}
	*(*uint64)(unsafe.Add(sp, 8*idx)) = v
}

// mdctPostTwiddleSSE is clt_mdct_forward_c's post-rotation for pairBlocks
// pairs of four-output blocks: forward outputs i..i+3 and their mirrors
// n4-4-i..n4-1-i, which together tile coeffs contiguously from both ends.
// yr = im*t1 - re*t0 lands at coeffs[2i] and yi = re*t1 + im*t0 at
// coeffs[n2-1-2i]. The caller finishes the n4%8 middle.
//
//go:noinline
func mdctPostTwiddleSSE(coeffs []float32, fftStage []kissCpx, trig []float32, n2, n4, pairBlocks int) {
	if pairBlocks == 0 {
		return
	}
	_ = coeffs[n2-1]
	_ = trig[2*n4-1]
	_ = fftStage[n4-1]
	if 8*pairBlocks > n4 {
		panic("mdct: post-twiddle blocks overlap")
	}
	cp := unsafe.Pointer(unsafe.SliceData(coeffs))
	fp := unsafe.Pointer(unsafe.SliceData(fftStage))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	for b := range pairBlocks {
		i := 4 * b
		m := n4 - 4 - i
		reF, imF := mdctLoadCpx4(unsafe.Add(fp, 8*i))
		t0F := loadF32x4(unsafe.Add(tp, 4*i))
		t1F := loadF32x4(unsafe.Add(tp, 4*(n4+i)))
		yrF := imF.Mul(t1F).Sub(reF.Mul(t0F))
		yiF := reF.Mul(t1F).Add(imF.Mul(t0F))
		if mdctUseFMALikeMixEnabled {
			yrF = imF.MulAdd(t1F, reF.Mul(t0F).Neg())
			yiF = reF.MulAdd(t1F, imF.Mul(t0F))
		}

		reM, imM := mdctLoadCpx4(unsafe.Add(fp, 8*m))
		t0M := loadF32x4(unsafe.Add(tp, 4*m))
		t1M := loadF32x4(unsafe.Add(tp, 4*(n4+m)))
		yrM := imM.Mul(t1M).Sub(reM.Mul(t0M))
		yiM := reM.Mul(t1M).Add(imM.Mul(t0M))
		if mdctUseFMALikeMixEnabled {
			yrM = imM.MulAdd(t1M, reM.Mul(t0M).Neg())
			yiM = reM.MulAdd(t1M, imM.Mul(t0M))
		}

		low := unsafe.Add(cp, 4*2*i)
		rYiM := yiM.ToBits().PermuteScalars(3, 2, 1, 0)
		storeF32x4(low, yrF.ToBits().InterleaveLo(rYiM).BitsToFloat32())
		storeF32x4(unsafe.Add(low, 16), yrF.ToBits().InterleaveHi(rYiM).BitsToFloat32())

		high := unsafe.Add(cp, 4*(n2-8-2*i))
		rYiF := yiF.ToBits().PermuteScalars(3, 2, 1, 0)
		storeF32x4(high, yrM.ToBits().InterleaveLo(rYiF).BitsToFloat32())
		storeF32x4(unsafe.Add(high, 16), yrM.ToBits().InterleaveHi(rYiF).BitsToFloat32())
	}
}

// mdctLoadCpx4 loads four interleaved {r, i} pairs and splits them into the
// real and imaginary lanes.
func mdctLoadCpx4(p unsafe.Pointer) (re, im archsimd.Float32x4) {
	a := loadF32x4(p)
	b := loadF32x4(unsafe.Add(p, 16))
	return a.ConcatPermuteScalars(0, 2, 4, 6, b), a.ConcatPermuteScalars(1, 3, 5, 7, b)
}

// mdctEvenAsc returns p[0], p[2], p[4], p[6].
func mdctEvenAsc(p unsafe.Pointer) archsimd.Float32x4 {
	return loadF32x4(p).ConcatPermuteScalars(0, 2, 4, 6, loadF32x4(unsafe.Add(p, 16)))
}

// mdctEvenDesc returns p[7], p[5], p[3], p[1]: four samples stepping down by
// two from p[7].
func mdctEvenDesc(p unsafe.Pointer) archsimd.Float32x4 {
	return loadF32x4(unsafe.Add(p, 16)).ConcatPermuteScalars(3, 1, 7, 5, loadF32x4(p))
}

// mdctRotateStore is clt_mdct_forward_c's pre-rotation of four folded
// (re, im) values: yr = re*t0 - im*t1, yi = im*t0 + re*t1, each scaled and
// stored at fftStage[bitrev[i..i+3]], as mdctStoreDirectStage does per element.
func mdctRotateStore(sp unsafe.Pointer, n int, br []int, t0, t1, s4, re, im archsimd.Float32x4) {
	yr := re.Mul(t0).Sub(im.Mul(t1)).Mul(s4)
	yi := im.Mul(t0).Add(re.Mul(t1)).Mul(s4)
	pairs01 := yr.ToBits().InterleaveLo(yi.ToBits()).ReshapeToUint64s()
	pairs23 := yr.ToBits().InterleaveHi(yi.ToBits()).ReshapeToUint64s()
	k := br[:4:4]
	mdctStorePair(sp, n, k[0], pairs01.GetElem(0))
	mdctStorePair(sp, n, k[1], pairs01.GetElem(1))
	mdctStorePair(sp, n, k[2], pairs23.GetElem(0))
	mdctStorePair(sp, n, k[3], pairs23.GetElem(1))
}

// mdctLeadFoldSSE is clt_mdct_forward_c's leading windowed fold and
// pre-rotation for 4*blocks outputs from i0. AMD64 v3 fuses the first source
// product and rounds the second for each fold output; AMD64 v1/v2 round both
// products. The SIMD pre-rotation rounds both products on every target. The
// loads read samples[xp1+n2 : xp1+n2+8*blocks], samples[xp2-n2-8*blocks+1 :
// xp2+1], window[wp1 : wp1+8*blocks] and window[wp2-8*blocks+1 : wp2+1].
//
//go:noinline
func mdctLeadFoldSSE(fftStage []kissCpx, bitrev []int, samples, window, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, scale float32) {
	_ = samples[xp1+n2+8*blocks-1]
	_ = samples[xp2]
	_ = samples[xp2-n2-8*blocks+1]
	_ = window[wp1+8*blocks-1]
	_ = window[wp2]
	_ = window[wp2-8*blocks+1]
	_ = trig[n4+i0+4*blocks-1]
	br := bitrev[i0 : i0+4*blocks]
	sp := unsafe.Pointer(unsafe.SliceData(samples))
	wp := unsafe.Pointer(unsafe.SliceData(window))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	fp := unsafe.Pointer(unsafe.SliceData(fftStage))
	n := len(fftStage)
	s4 := broadcastF32x4Arch(scale)
	for b := range blocks {
		d := 8 * b
		a := mdctEvenAsc(unsafe.Add(sp, 4*(xp1+n2+d)))
		a2 := mdctEvenAsc(unsafe.Add(sp, 4*(xp1+d)))
		wC := mdctEvenAsc(unsafe.Add(wp, 4*(wp1+d)))
		bv := mdctEvenDesc(unsafe.Add(sp, 4*(xp2-d-7)))
		b2 := mdctEvenDesc(unsafe.Add(sp, 4*(xp2-n2-d-7)))
		wD := mdctEvenDesc(unsafe.Add(wp, 4*(wp2-d-7)))
		re := a.Mul(wD).Add(bv.Mul(wC))
		im := a2.Mul(wC).Sub(b2.Mul(wD))
		if mdctUseFMALikeMixEnabled {
			// clt_mdct_forward_c() contracts the first source product and
			// rounds the second product before the add/subtract.
			re = a.MulAdd(wD, bv.Mul(wC))
			im = a2.MulAdd(wC, b2.Mul(wD).Neg())
		}
		i := i0 + 4*b
		mdctRotateStore(fp, n, br[4*b:], loadF32x4(unsafe.Add(tp, 4*i)), loadF32x4(unsafe.Add(tp, 4*(n4+i))), s4, re, im)
	}
}

// mdctTailFoldSSE is clt_mdct_forward_c's trailing windowed fold and
// pre-rotation for 4*blocks outputs from i0. AMD64 v3 contracts the second
// source product for the real fold output and the first for the imaginary
// output; AMD64 v1/v2 round both fold products. The SIMD pre-rotation rounds
// both products on every target. The
// loads read samples[xp1-n2 : xp1+8*blocks], samples[xp2-8*blocks+1 :
// xp2+n2+1], window[wp1 : wp1+8*blocks] and window[wp2-8*blocks+1 : wp2+1].
//
//go:noinline
func mdctTailFoldSSE(fftStage []kissCpx, bitrev []int, samples, window, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, scale float32) {
	_ = samples[xp1-n2]
	_ = samples[xp1+8*blocks-1]
	_ = samples[xp2+n2]
	_ = samples[xp2-8*blocks+1]
	_ = window[wp1+8*blocks-1]
	_ = window[wp2]
	_ = window[wp2-8*blocks+1]
	_ = trig[n4+i0+4*blocks-1]
	br := bitrev[i0 : i0+4*blocks]
	sp := unsafe.Pointer(unsafe.SliceData(samples))
	wp := unsafe.Pointer(unsafe.SliceData(window))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	fp := unsafe.Pointer(unsafe.SliceData(fftStage))
	n := len(fftStage)
	s4 := broadcastF32x4Arch(scale)
	for b := range blocks {
		d := 8 * b
		a3 := mdctEvenAsc(unsafe.Add(sp, 4*(xp1-n2+d)))
		a2 := mdctEvenAsc(unsafe.Add(sp, 4*(xp1+d)))
		wC := mdctEvenAsc(unsafe.Add(wp, 4*(wp1+d)))
		bv := mdctEvenDesc(unsafe.Add(sp, 4*(xp2-d-7)))
		b4 := mdctEvenDesc(unsafe.Add(sp, 4*(xp2+n2-d-7)))
		wD := mdctEvenDesc(unsafe.Add(wp, 4*(wp2-d-7)))
		re := bv.Mul(wD).Sub(a3.Mul(wC))
		im := a2.Mul(wD).Add(b4.Mul(wC))
		if mdctUseFMALikeMixEnabled {
			// The leading minus in the C expression contracts with the second
			// product; the other output fuses its first source product.
			re = bv.MulAdd(wD, a3.Mul(wC).Neg())
			im = a2.MulAdd(wD, b4.Mul(wC))
		}
		i := i0 + 4*b
		mdctRotateStore(fp, n, br[4*b:], loadF32x4(unsafe.Add(tp, 4*i)), loadF32x4(unsafe.Add(tp, 4*(n4+i))), s4, re, im)
	}
}

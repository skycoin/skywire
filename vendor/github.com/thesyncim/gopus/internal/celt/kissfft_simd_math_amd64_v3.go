//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// These operations mirror GCC's contraction in celt/kiss_fft.c at
// -O3 -march=x86-64-v3: the first product remains fused and the second rounds.
func bflyMulSource4AMD64(ar, ai, wr, wi archsimd.Float32x4) (re, im archsimd.Float32x4) {
	re = ar.MulAdd(wr, negF32x4AVX(ai.Mul(wi)))
	im = ar.MulAdd(wi, ai.Mul(wr))
	return re, im
}

func bflyMulSource4UnfusedAMD64(ar, ai, wr, wi archsimd.Float32x4) (re, im archsimd.Float32x4) {
	return ar.Mul(wr).Sub(ai.Mul(wi)), ar.Mul(wi).Add(ai.Mul(wr))
}

func bflyMulSource4FusedAMD64(ar, ai, wr, wi archsimd.Float32x4) (re, im archsimd.Float32x4) {
	return bflyMulSource4AMD64(ar, ai, wr, wi)
}

func bflyMulAddSource4AMD64(a, b, c, d archsimd.Float32x4) archsimd.Float32x4 {
	return a.MulAdd(b, c.Mul(d))
}

func bflyMulSubSource4AMD64(a, b, c, d archsimd.Float32x4) archsimd.Float32x4 {
	return a.MulAdd(b, negF32x4AVX(c.Mul(d)))
}

func bflyMulAdd4AMD64(a, b, c archsimd.Float32x4) archsimd.Float32x4 {
	return a.MulAdd(b, c)
}

// celt/kiss_fft.c's radix-3 half subtraction contracts only in the AVX
// vectorized groups of the GCC v3 build.
func bflyHalfSub4AMD64(a, b, half archsimd.Float32x4) archsimd.Float32x4 {
	return negF32x4AVX(half).MulAdd(b, a)
}

// GCC vectorizes celt/kiss_fft.c's radix-2 m==4 loop in four-group blocks.
// The final one to three groups use the scalar tail, whose twiddle products
// round before the output add/subtract.
func bfly2M4GroupOutputsAMD64(fr, fi, r, i, tw archsimd.Float32x4, lane0, lane1, lane2, lane3 archsimd.Mask32x4, fused bool) (minusR, minusI, plusR, plusI archsimd.Float32x4) {
	sumRI := r.Add(i)
	difIR := i.Sub(r)
	sumIR := i.Add(r)
	tr := r.IfElse(lane0, sumRI.Mul(tw).IfElse(lane1, i.IfElse(lane2, difIR.Mul(tw))))
	ti := i.IfElse(lane0, difIR.Mul(tw).IfElse(lane1, negF32x4AVX(r).IfElse(lane2, negF32x4AVX(sumIR.Mul(tw)))))
	minusR, minusI, plusR, plusI = fr.Sub(tr), fi.Sub(ti), fr.Add(tr), fi.Add(ti)
	if !fused {
		return minusR, minusI, plusR, plusI
	}
	trFMA := r.IfElse(lane0, sumRI.IfElse(lane1, i.IfElse(lane2, difIR)))
	tiFMA := i.IfElse(lane0, difIR.IfElse(lane1, negF32x4AVX(r).IfElse(lane2, negF32x4AVX(sumIR))))
	minusRFMA := negF32x4AVX(tw).MulAdd(trFMA, fr)
	minusIFMA := negF32x4AVX(tw).MulAdd(tiFMA, fi)
	plusRFMA := tw.MulAdd(trFMA, fr)
	plusIFMA := tw.MulAdd(tiFMA, fi)
	lane13 := lane1.Or(lane3)
	minusR = minusRFMA.IfElse(lane13, minusR)
	minusI = minusIFMA.IfElse(lane13, minusI)
	plusR = plusRFMA.IfElse(lane13, plusR)
	plusI = plusIFMA.IfElse(lane13, plusI)
	return minusR, minusI, plusR, plusI
}

//go:build amd64 && goexperiment.simd && !nosimd && !purego && !amd64.v3

package celt

import "simd/archsimd"

func bflyMulSource4AMD64(ar, ai, wr, wi archsimd.Float32x4) (re, im archsimd.Float32x4) {
	return ar.Mul(wr).Sub(ai.Mul(wi)), ar.Mul(wi).Add(ai.Mul(wr))
}

func bflyMulSource4UnfusedAMD64(ar, ai, wr, wi archsimd.Float32x4) (re, im archsimd.Float32x4) {
	return bflyMulSource4AMD64(ar, ai, wr, wi)
}

func bflyMulSource4FusedAMD64(ar, ai, wr, wi archsimd.Float32x4) (re, im archsimd.Float32x4) {
	return bflyMulSource4AMD64(ar, ai, wr, wi)
}

func bflyMulAddSource4AMD64(a, b, c, d archsimd.Float32x4) archsimd.Float32x4 {
	return a.Mul(b).Add(c.Mul(d))
}

func bflyMulSubSource4AMD64(a, b, c, d archsimd.Float32x4) archsimd.Float32x4 {
	return a.Mul(b).Sub(c.Mul(d))
}

func bflyMulAdd4AMD64(a, b, c archsimd.Float32x4) archsimd.Float32x4 {
	return a.Mul(b).Add(c)
}

func bflyHalfSub4AMD64(a, b, half archsimd.Float32x4) archsimd.Float32x4 {
	return a.Sub(half.Mul(b))
}

func bfly2M4GroupOutputsAMD64(fr, fi, r, i, tw archsimd.Float32x4, lane0, lane1, lane2, lane3 archsimd.Mask32x4, fused bool) (minusR, minusI, plusR, plusI archsimd.Float32x4) {
	_ = fused
	sumRI := r.Add(i)
	difIR := i.Sub(r)
	sumIR := i.Add(r)
	tr := r.IfElse(lane0, sumRI.Mul(tw).IfElse(lane1, i.IfElse(lane2, difIR.Mul(tw))))
	ti := i.IfElse(lane0, difIR.Mul(tw).IfElse(lane1, negF32x4AVX(r).IfElse(lane2, negF32x4AVX(sumIR.Mul(tw)))))
	return fr.Sub(tr), fi.Sub(ti), fr.Add(tr), fi.Add(ti)
}

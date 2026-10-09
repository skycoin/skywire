//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// spreadCountThresholds counts how many coefficients x[0..n-1] satisfy
// x[j]*x[j]*nf < threshold for three thresholds (0.25, 0.0625, 0.015625).
// Four lanes run the scalar per-coefficient products and comparisons; each
// lane's true comparison adds one to that threshold's count, as the
// vectorized spreading_decision() loop of the libopus SIMD build does.
func spreadCountThresholds(x []celtNorm, n int, nf float32) (t0, t1, t2 int) {
	x = x[:n]
	if !archsimd.X86.AVX() {
		return spreadCountThresholdsScalar(x, nf)
	}
	return spreadCountThresholdsAVX(x, n, nf)
}

//go:noinline
func spreadCountThresholdsAVX(x []celtNorm, n int, nf float32) (t0, t1, t2 int) {
	x = x[:n]
	blocks := n &^ 3
	if blocks > 0 {
		nf4 := broadcastF32x4Arch(nf)
		c0 := broadcastF32x4Arch(spreadThresholds[0])
		c1 := broadcastF32x4Arch(spreadThresholds[1])
		c2 := broadcastF32x4Arch(spreadThresholds[2])
		var n0, n1, n2 archsimd.Int32x4
		p := unsafe.Pointer(unsafe.SliceData(x))
		for j := 0; j < blocks; j += 4 {
			v := loadF32x4(unsafe.Add(p, 4*j))
			x2N := v.Mul(v).Mul(nf4)
			n0 = n0.Sub(x2N.Less(c0).ToInt32x4())
			n1 = n1.Sub(x2N.Less(c1).ToInt32x4())
			n2 = n2.Sub(x2N.Less(c2).ToInt32x4())
		}
		t0 = int(n0.GetElem(0) + n0.GetElem(1) + n0.GetElem(2) + n0.GetElem(3))
		t1 = int(n1.GetElem(0) + n1.GetElem(1) + n1.GetElem(2) + n1.GetElem(3))
		t2 = int(n2.GetElem(0) + n2.GetElem(1) + n2.GetElem(2) + n2.GetElem(3))
	}
	r0, r1, r2 := spreadCountThresholdsScalar(x[blocks:], nf)
	return t0 + r0, t1 + r1, t2 + r2
}

//go:build arm64 && (nosimd || purego || !goexperiment.simd)

package celt

// imdctPreRotateFMA32Kiss is the arm64 nosimd form of the FMA-like IMDCT
// pre-rotation. It matches the arm64 SIMD kernel exactly: each output fuses
// its first product into the add and rounds the second product on its own,
// matching the clang -ffp-contract=on float path of libopus
// clt_mdct_backward_c(). It fuses through fma32 (a*b+c); on arm64 the backend
// contracts a*b+c into one single-rounding FMADDS, which
// float32(math.FMA(a,b,c)) does not always reproduce because it rounds twice. The
// kernel runs on arm64 without SIMD (mdctUseFMALikeMixEnabled); the SIMD path
// supplies the matching fused rotation. The libopus-oracle parity suite
// and TestIMDCTPreRotateFMA32KissMatchesScalar gate the contraction.
func imdctPreRotateFMA32Kiss(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	if n4 <= 0 {
		return
	}
	_ = spectrum[n2-1]
	_ = trig[n2-1]
	_ = fftIn[n4-1]
	for i := 0; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		fftIn[i] = complex(
			fma32(x1, t0, -noFMA32Mul(x2, t1)),
			fma32(x2, t0, noFMA32Mul(x1, t1)),
		)
	}
}

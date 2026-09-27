//go:build !arm64

package celt

// imdctPreRotateFMA32Kiss is the portable form of the FMA-like IMDCT
// pre-rotation. It mirrors the arm64 assembly kernel exactly: each output fuses
// its first product into the add and rounds the second product on its own,
// matching the clang -ffp-contract=on float path of libopus
// clt_mdct_backward_c(). The arm64 build supplies an assembly version; purego on
// arm64 uses this path so it fuses identically via math.FMA instead of relying
// on compiler contraction, which Go does not guarantee for a*b+c.
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
			mdctFMA32(x1, t0, -noFMA32Mul(x2, t1)),
			mdctFMA32(x2, t0, noFMA32Mul(x1, t1)),
		)
	}
}

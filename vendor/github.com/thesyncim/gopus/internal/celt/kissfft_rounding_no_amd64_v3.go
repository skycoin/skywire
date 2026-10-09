//go:build !amd64.v3

package celt

const kissFFTTargetV3FMA = false

func kissHalfSub(a, b float32) float32 {
	return a - round32(0.5*b)
}

func kissRadix3ScaledOutputs(f1r, f1i, s0r, s0i, epi3i float32) (fout1r, fout1i, fout2r, fout2i float32) {
	s0r = kissScaleMul(s0r, epi3i)
	s0i = kissScaleMul(s0i, epi3i)
	return f1r - s0i, f1i + s0r, f1r + s0i, f1i - s0r
}

func kissBfly2M4Outputs(base, product, twiddle float32) (minus, plus float32) {
	rounded := kissScaleMul(product, twiddle)
	return kissSub(base, rounded), kissAdd(base, rounded)
}

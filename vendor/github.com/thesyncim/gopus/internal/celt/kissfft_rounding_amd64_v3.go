//go:build amd64.v3

package celt

const kissFFTTargetV3FMA = true

// kissHalfSub matches HALF_OF followed by SUB32_ovflw in celt/kiss_fft.c's
// kf_bfly3: GCC contracts the 0.5 multiply into the subtraction for GOAMD64=v3.
func kissHalfSub(a, b float32) float32 {
	return fma32(-0.5, b, a)
}

// kissRadix3ScaledOutputs matches the four output assignments after
// C_MULBYSCALAR in celt/kiss_fft.c's scalar kf_bfly3. GCC contracts each
// scalar-twiddle product into its adjacent output add/subtract at v3.
func kissRadix3ScaledOutputs(f1r, f1i, s0r, s0i, epi3i float32) (fout1r, fout1i, fout2r, fout2i float32) {
	return fma32(-s0i, epi3i, f1r), fma32(s0r, epi3i, f1i),
		fma32(s0i, epi3i, f1r), fma32(-s0r, epi3i, f1i)
}

// kissBfly2M4Outputs matches the twiddled kf_bfly2 m==4 assignments in
// celt/kiss_fft.c, where GCC fuses the twiddle product into each output.
func kissBfly2M4Outputs(base, product, twiddle float32) (minus, plus float32) {
	return fma32(-product, twiddle, base), fma32(product, twiddle, base)
}

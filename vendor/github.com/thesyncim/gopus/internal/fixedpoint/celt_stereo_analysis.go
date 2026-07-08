//go:build gopus_fixed_point

package fixedpoint

// StereoAnalysis ports celt/celt_encoder.c stereo_analysis (FIXED_POINT): the
// L1-norm L/R vs M/S entropy comparison that picks dual-stereo for LM!=0 frames.
// X is the interleaved normalised spectrum (celt_norm), N0 the per-channel
// stride, eBands the mode band edges, nbEBands unused beyond bounds.
func StereoAnalysis(eBands []int16, X []int32, LM, N0, nbEBands int) bool {
	sumLR := int32(1) // EPSILON
	sumMS := int32(1)
	for i := 0; i < 13; i++ {
		for j := int(eBands[i]) << LM; j < int(eBands[i+1])<<LM; j++ {
			L := shr32(X[j], normShift-14)
			R := shr32(X[N0+j], normShift-14)
			Mv := add32(L, R)
			S := sub32(L, R)
			sumLR = add32(sumLR, add32(abs32(L), abs32(R)))
			sumMS = add32(sumMS, add32(abs32(Mv), abs32(S)))
		}
	}
	sumMS = mult16x32Q15(23170, sumMS) // QCONST16(0.707107f,15)
	thetas := 13
	if LM <= 1 {
		thetas -= 8
	}
	return mult16x32Q15(int16((int(eBands[13])<<(LM+1))+thetas), sumMS) >
		mult16x32Q15(int16(int(eBands[13])<<(LM+1)), sumLR)
}

//go:build !arm64 || purego

package celt

// deemphasisStereoPlanarF32Core mirrors the libopus celt/celt_decoder.c float
// deemphasis: tmp = in + VERY_SMALL + mem; mem = coef*tmp; out = scale*tmp,
// with the state update emitted as a standalone FMUL followed by a separate
// FADD into the next sample (matching the arm64 assembly and the clang
// reference). The products run through mul32 so the arm64 compiler cannot
// contract mem = coef*tmp into the following add as a single FMADD; on other
// targets mul32 is a plain multiply, leaving the scalar reference unchanged.
func deemphasisStereoPlanarF32Core(dst []float32, left, right []float32, n int, scale, stateL, stateR, coef, verySmall float32) (float32, float32) {
	_ = dst[n*2-1]
	_ = left[n-1]
	_ = right[n-1]
	i := 0
	j := 0
	for ; i+3 < n; i, j = i+4, j+8 {
		tmpL0 := left[i] + verySmall + stateL
		stateL = mul32(coef, tmpL0)
		tmpR0 := right[i] + verySmall + stateR
		stateR = mul32(coef, tmpR0)
		dst[j] = mul32(tmpL0, scale)
		dst[j+1] = mul32(tmpR0, scale)

		tmpL1 := left[i+1] + verySmall + stateL
		stateL = mul32(coef, tmpL1)
		tmpR1 := right[i+1] + verySmall + stateR
		stateR = mul32(coef, tmpR1)
		dst[j+2] = mul32(tmpL1, scale)
		dst[j+3] = mul32(tmpR1, scale)

		tmpL2 := left[i+2] + verySmall + stateL
		stateL = mul32(coef, tmpL2)
		tmpR2 := right[i+2] + verySmall + stateR
		stateR = mul32(coef, tmpR2)
		dst[j+4] = mul32(tmpL2, scale)
		dst[j+5] = mul32(tmpR2, scale)

		tmpL3 := left[i+3] + verySmall + stateL
		stateL = mul32(coef, tmpL3)
		tmpR3 := right[i+3] + verySmall + stateR
		stateR = mul32(coef, tmpR3)
		dst[j+6] = mul32(tmpL3, scale)
		dst[j+7] = mul32(tmpR3, scale)
	}
	for ; i < n; i, j = i+1, j+2 {
		tmpL := left[i] + verySmall + stateL
		stateL = mul32(coef, tmpL)
		tmpR := right[i] + verySmall + stateR
		stateR = mul32(coef, tmpR)
		dst[j] = mul32(tmpL, scale)
		dst[j+1] = mul32(tmpR, scale)
	}
	return stateL, stateR
}

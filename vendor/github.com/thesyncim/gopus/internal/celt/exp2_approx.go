package celt

import "github.com/thesyncim/gopus/internal/opusmath"

// celtExp2 approximates exp2(x) using libopus FLOAT_APPROX polynomial.
// This matches tmp_check/opus-1.6.1/celt/mathops.h (float path).
func celtExp2(x float32) float32 {
	return opusmath.CeltExp2(x)
}

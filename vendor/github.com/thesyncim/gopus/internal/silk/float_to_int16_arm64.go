//go:build arm64 && !purego

package silk

// floatToInt16ScaledCore writes out[i] = sat16(round_even(in[i]*scale)) for the
// first n elements with NEON (n must be a multiple of 8). FCVTNS+SQXTN matches
// the scalar saturate-then-round-even result bit-for-bit, so this stays
// byte-exact on every target.
//
//go:noescape
func floatToInt16ScaledCore(out []int16, in []float32, scale float32, n int)

// floatToInt16Scaled vectorizes the bulk of the conversion and finishes the
// sub-block remainder with the scalar reference.
func floatToInt16Scaled(out []int16, in []float32, scale float32, n int) {
	n8 := n &^ 7
	if n8 > 0 {
		floatToInt16ScaledCore(out, in, scale, n8)
	}
	for i := n8; i < n; i++ {
		out[i] = floatToInt16Round(in[i] * scale)
	}
}

//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package opusmath

// celtLog2NormalizeF32 matches the contraction in celt/mathops.h:celt_log2
// for the default float AMD64 v3 target.
func celtLog2NormalizeF32(x, scale float32) float32 {
	return fma32(x, scale, -1.0625)
}

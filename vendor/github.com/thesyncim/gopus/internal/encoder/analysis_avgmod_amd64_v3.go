//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

const analysisAvgModFMAEnabled = true

// analysisAvgMod32 matches the GCC x86-64-v3 contraction at analysis.c:599.
// The first square is rounded by the caller; this helper keeps the final
// square and old-history addition in registers for one float32 FMA. Doubling
// the bounded mod2 fourth power is exact before its addition.
//
//go:noinline
func analysisAvgMod32(mod1Square, oldD2Angle, mod2Fourth float32) float32 {
	return 0.25 * (mod1Square*mod1Square + oldD2Angle + 2*mod2Fourth)
}

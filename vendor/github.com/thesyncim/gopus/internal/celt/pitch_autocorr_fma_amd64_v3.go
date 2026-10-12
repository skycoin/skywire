//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

const pitchAutocorrUsesFMA32 = true

// The ordinary GCC 13.3 x86-v3 SIMD celt/celt_lpc.c::_celt_autocorr caller
// processes four-term groups with rounded vector products and ordered adds,
// then contracts only its one-to-three scalar residual MAC16_16 updates. The
// caller selects this helper only when the residual length is below four. Keep
// the operands at a register boundary so Go emits the matching float32 FMA.
//
//go:noinline
func pitchAutocorrMAC32(a, b, c float32) float32 {
	return a*b + c
}

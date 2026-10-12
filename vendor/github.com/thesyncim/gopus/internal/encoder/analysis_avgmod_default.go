//go:build !amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package encoder

const analysisAvgModFMAEnabled = false

// analysisAvgMod32 preserves the separate float32 square used by the source
// path outside the validated default amd64.v3 build.
func analysisAvgMod32(mod1Square, oldD2Angle, mod2Fourth float32) float32 {
	mod1Fourth := round32(mod1Square * mod1Square)
	return 0.25 * (oldD2Angle + mod1Fourth + 2*mod2Fourth)
}

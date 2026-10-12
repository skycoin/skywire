//go:build !amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package encoder

// analysisWeightedProduct32 preserves the compiler's default float behavior
// outside the validated default amd64.v3 build.
func analysisWeightedProduct32(a, b float32) float32 { return a * b }

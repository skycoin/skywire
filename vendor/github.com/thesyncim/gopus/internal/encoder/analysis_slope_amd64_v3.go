//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

// analysisSlopeAccumulate preserves the separate float32 multiply and add
// emitted for tonality_analysis's slope update by the pinned libopus 1.6.1
// GCC 13.3 amd64.v3 scalar and SIMD callers in src/analysis.c.
func analysisSlopeAccumulate(slope, bandTonality, bandWeight float32) float32 {
	return slope + round32(bandTonality*bandWeight)
}

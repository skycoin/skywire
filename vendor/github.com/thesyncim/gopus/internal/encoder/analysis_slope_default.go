//go:build !(amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes)

package encoder

// analysisSlopeAccumulate retains the direct slope expression outside the
// matched default amd64.v3 float lanes.
func analysisSlopeAccumulate(slope, bandTonality, bandWeight float32) float32 {
	return slope + bandTonality*bandWeight
}

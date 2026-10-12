//go:build !(amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes)

package encoder

// analysisSpecVariabilityAddSquare keeps the direct source expression outside
// the paired default-float amd64.v3 reference lanes.
func analysisSpecVariabilityAddSquare(acc, difference float32) float32 {
	return acc + difference*difference
}

func analysisSpecVariabilityAddFinalSquare(acc, difference float32) float32 {
	return acc + difference*difference
}

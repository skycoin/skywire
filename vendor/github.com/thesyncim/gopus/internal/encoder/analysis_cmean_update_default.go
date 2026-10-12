//go:build !(amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes)

package encoder

// analysisCMeanUpdate retains the source expression outside the matched
// default float AMD64 v3 configuration.
func analysisCMeanUpdate(alpha, old, bfcc float32) float32 {
	return fma32(1.0-alpha, old, round32(alpha*bfcc))
}

func analysisStdUpdate(alpha float32, std *[9]float32, features *[25]float32) {
	for i := range std {
		std[i] = fma32(1.0-alpha, std[i], round32(alpha*features[i]*features[i]))
	}
}

// analysisCMeanFeatureTail retains the direct source expression outside the
// matched default-float AMD64 v3 configuration.
func analysisCMeanFeatureTail(feature, oldCMean float32) float32 {
	return feature - 1.4349*oldCMean
}

// analysisFeatureMemoryTail retains the direct source expression outside the
// matched default-float AMD64 v3 configuration.
func analysisFeatureMemoryTail(feature, mem8 float32) float32 {
	return feature - 0.53452*mem8
}

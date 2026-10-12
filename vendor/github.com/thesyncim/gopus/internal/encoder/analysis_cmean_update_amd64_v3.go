//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

// analysisCMeanUpdate follows the pinned src/analysis.c update for the default
// float AMD64 v3 build: alpha*BFCC is rounded before the fused old-mean term.
func analysisCMeanUpdate(alpha, old, bfcc float32) float32 {
	product := round32(alpha * bfcc)
	return analysisCMeanFMA32(1-alpha, old, product)
}

// analysisStdUpdate matches the recurrence compiled from pinned src/analysis.c:910.
// Its alpha*feature products round before GCC fuses the old-Std term and
// product. One whole-array boundary keeps the confirmed fused updates in one
// call per analysis chunk while allowing the Go compiler to emit the FMA.
//
//go:noinline
func analysisStdUpdate(alpha float32, std *[9]float32, features *[25]float32) {
	beta := 1 - alpha
	for i := range std {
		product := round32(alpha * features[i] * features[i])
		std[i] = beta*std[i] + product
	}
}

// analysisCMeanFeatureTail follows the final fused term in pinned src/analysis.c:897.
func analysisCMeanFeatureTail(feature, oldCMean float32) float32 {
	return fma32(-1.4349, oldCMean, feature)
}

// analysisFeatureMemoryTail follows the fused final term in pinned src/analysis.c:905.
func analysisFeatureMemoryTail(feature, mem8 float32) float32 {
	return fma32(-0.53452, mem8, feature)
}

// The noinline boundary emits CMean's update as one scalar FMA after round32
// materializes alpha*BFCC, matching the fused term in src/analysis.c:900.
//
//go:noinline
func analysisCMeanFMA32(beta, old, roundedProduct float32) float32 {
	return beta*old + roundedProduct
}

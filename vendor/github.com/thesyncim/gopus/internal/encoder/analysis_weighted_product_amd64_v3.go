//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import "math"

// analysisWeightedProduct32 rounds each C-float product before the separate
// accumulation in src/analysis.c:tonality_get_info. The matched v3 C getter
// rounds both the weighted probability products and the VAD bias product.
func analysisWeightedProduct32(a, b float32) float32 {
	product := a * b
	return math.Float32frombits(math.Float32bits(product))
}

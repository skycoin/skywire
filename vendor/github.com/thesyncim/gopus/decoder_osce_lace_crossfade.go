//go:build gopus_osce

package gopus

// osceLACECrossFade10ms follows dnn/osce_features.c:osce_cross_fade_10ms.
// The second post-reset frame blends its first 160 samples against the raw
// lowband; its trailing 160 samples retain the enhanced output. The raw product
// rounds before the addition, matching C's enhanced-product FMA on arm64 and
// separate operations on amd64.
func osceLACECrossFade10ms(xEnhanced, xIn []float32, length int) {
	if length < 160 {
		return
	}
	if len(xEnhanced) < 160 || len(xIn) < 160 {
		return
	}
	for i := 0; i < 160; i++ {
		w := osceWindow[i]
		raw := float32((1.0 - w) * xIn[i])
		xEnhanced[i] = w*xEnhanced[i] + raw
	}
}

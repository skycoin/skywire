//go:build amd64.v3 && !gopus_fixed_point

package silk

// silkLTPFNMADD32 matches the scalar contraction in
// silk/float/LTP_analysis_filter_FLP.c: residual -= tap * lag. The Go input is
// normalized, so scale converts each lag to the C-domain value before the FMA.
// Keeping this helper inlineable avoids a per-sample call.
func silkLTPFNMADD32(tap, rawLag, scale, residual float32) float32 {
	lag := rawLag * scale
	return silkFMA32(-tap, lag, residual)
}

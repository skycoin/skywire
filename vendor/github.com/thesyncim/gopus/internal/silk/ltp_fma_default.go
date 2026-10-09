//go:build !amd64.v3 || gopus_fixed_point

package silk

// silkLTPFNMADD32 computes residual - (tap*rawLag)*scale on non-v3 and
// fixed-point targets. The float path in silk/float/LTP_analysis_filter_FLP.c
// supplies the corresponding int16-domain lag values to its filter.
func silkLTPFNMADD32(tap, rawLag, scale, residual float32) float32 {
	return residual - tap*rawLag*scale
}

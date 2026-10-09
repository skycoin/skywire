//go:build amd64.v3 && !gopus_fixed_point

package encoder

// stereoWidthDecorrelation matches the fused 1-corr*corr term in the pinned
// src/opus_encoder.c:compute_stereo_width float path for AMD64 v3.
//
//go:noinline
func stereoWidthDecorrelation(corr opusVal32) opusVal32 {
	return -corr*corr + 1
}

//go:noinline
func stereoWidthXYUpdate(beta, oldXY, roundedAlphaXY opusVal32) opusVal32 {
	// Keep the caller-rounded alpha*xy operand and fuse beta*oldXY with it,
	// matching the pinned AMD64 v3 src/opus_encoder.c:compute_stereo_width.
	return beta*oldXY + roundedAlphaXY
}

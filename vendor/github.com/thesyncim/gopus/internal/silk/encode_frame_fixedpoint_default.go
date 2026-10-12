//go:build !gopus_fixed_point

package silk

// silkEncoderFixedFields is empty in the default (float) build, keeping the
// integer SILK encode path unlinked.
type silkEncoderFixedFields struct{}

// fixedEncodeActive reports whether the integer SILK encode path is selected.
// Always false in the default build.
func (e *Encoder) fixedEncodeActive() bool { return false }

// silkFixedEncodeBuild reports whether the integer SILK encode path is compiled
// in. False in the default (float) build.
const silkFixedEncodeBuild = false

// encodeFrameFixedBody is never reached in the default build (fixedEncodeActive
// returns false); the stub keeps encodeFrame build-tag agnostic.
func (e *Encoder) encodeFrameFixedBody(_ []int16, _, _, _ int, _, _ bool, _ int, _ bool) int32 {
	return 0
}

// prefillFrameFixed is never reached in the default build (fixedEncodeActive
// returns false).
func (e *Encoder) prefillFrameFixed(_ []int16) {}

// resetFixedState is a no-op in the default build.
func (e *Encoder) resetFixedState() {}

// resetFixedAnalysisHistory is a no-op in the default build.
func (e *Encoder) resetFixedAnalysisHistory() {}

// xBufToInt16 is the silk_float2short_array of x_buf in silk_setup_resamplers
// (silk/control_codec.c): it rounds the first len(dst) samples of x_buf, kept
// normalized to [-1, 1], to int16.
func (e *Encoder) xBufToInt16(dst []int16) {
	for k := range dst {
		dst[k] = float32ToInt16(e.xBuf[k])
	}
}

// xBufFromInt16 is the silk_short2float_array back into x_buf in
// silk_setup_resamplers (silk/control_codec.c).
func (e *Encoder) xBufFromInt16(src []int16) {
	for k, v := range src {
		e.xBuf[k] = float32(v) * (1.0 / silkSampleScale)
	}
}

//go:build !gopus_fixed_point

package silk

// silkEncoderFixedFields is empty in the default (float) build, keeping the
// Encoder struct byte-unchanged and the integer SILK encode path unlinked.
type silkEncoderFixedFields struct{}

// fixedEncodeActive reports whether the integer SILK encode path is selected.
// Always false in the default build.
func (e *Encoder) fixedEncodeActive() bool { return false }

// silkFixedEncodeBuild reports whether the integer SILK encode path is compiled
// in. False in the default (float) build.
const silkFixedEncodeBuild = false

// encodeFrameFixedBody is never reached in the default build (fixedEncodeActive
// returns false); the stub keeps EncodeFrame build-tag agnostic.
func (e *Encoder) encodeFrameFixedBody(_ []float32, _, _, _, _, _ int, _, _, _, _ bool) []byte {
	return nil
}

// resetFixedState is a no-op in the default build.
func (e *Encoder) resetFixedState() {}

// stereoFrontEnd uses the float StereoLRToMSWithRates analysis in the default
// build; the integer mid/side outputs are nil (unused on this path).
func (e *Encoder) stereoFrontEnd(
	left, right []float32,
	frameLength, fsKHz int,
	totalRateBps int,
	prevSpeechActQ8 int32,
	toMono bool,
) (midOut, sideOut []float32, midI16, sideI16 []int16, ix StereoQuantIndices, midOnly bool, midRate, sideRate int, widthQ14 int16) {
	midOut, sideOut, ix, midOnly, midRate, sideRate, widthQ14 = e.StereoLRToMSWithRates(
		left, right, frameLength, fsKHz, totalRateBps, prevSpeechActQ8, toMono,
	)
	return midOut, sideOut, nil, nil, ix, midOnly, midRate, sideRate, widthQ14
}

// stageStereoInt16 is a no-op in the default build (float mid/side feed the
// per-channel encode directly).
func (e *Encoder) stageStereoInt16(_ []int16) {}

// stereoSideVADFixed reports that no integer side-VAD decision is available in
// the default build; callers fall back to analyzer/external VAD flags.
func (e *Encoder) stereoSideVADFixed(_ *Encoder, _ []int16, _, _ int, _ bool) (active, ok bool) {
	return false, false
}

// FixedLastVADFlag is always false in the default build (no integer encode path).
func (e *Encoder) FixedLastVADFlag() bool { return false }

// resetStereoSideFixedState is a no-op in the default build.
func (e *Encoder) resetStereoSideFixedState() {}

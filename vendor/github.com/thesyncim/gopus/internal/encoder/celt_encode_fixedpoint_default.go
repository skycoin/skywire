//go:build !gopus_fixed_point

package encoder

// fixedPointBuild is false in the default (float) build.
const fixedPointBuild = false

// encoderFixedCELTFields is empty in the default (float) build, keeping the
// Encoder struct byte-unchanged.
type encoderFixedCELTFields struct{}

// encodeCELTFrameFixed never handles a frame in the default build, so the CELT
// frame seam always uses the float celt.Encoder. This keeps the dispatch in
// encodeCELTFrameWithBitrateMaxPayloadAndDRED build-tag agnostic.
func (e *Encoder) encodeCELTFrameFixed(_ []opusRes, _, _, _ int) ([]byte, bool, error) {
	return nil, false, nil
}

// resetFixedCELT is a no-op in the default build.
func (e *Encoder) resetFixedCELT() {}

// fixedCELTFinalRange never reports an integer range in the default build.
func (e *Encoder) fixedCELTFinalRange() (uint32, bool) { return 0, false }

// clearFixedCELTUsed is a no-op in the default build.
func (e *Encoder) clearFixedCELTUsed() {}

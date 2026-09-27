//go:build !arm64 || purego

package celt

// mdctUseNeonMidFold is false off the fused arm64 build; the scalar
// mdctStoreDirectStage* loops run everywhere else (and remain the byte-exact
// purego oracle path).
const mdctUseNeonMidFold = false

// mdctMidFoldStoreNeon is never called off arm64 (guarded by
// mdctUseNeonMidFold); the stub keeps the package building on all targets.
func mdctMidFoldStoreNeon(dst []kissCpx, bitrev []int, samples []float32, trig []float32, i0, n4, xp1, xp2, blocks int, preScale float32) {
	for b := 0; b < blocks; b++ {
		for lane := 0; lane < 4; lane++ {
			j := 4*b + lane
			re := samples[xp2-2*j]
			im := samples[xp1+2*j]
			mdctStoreDirectStageFMALike(dst, bitrev[i0+j], preScale, re, im, trig[i0+j], trig[n4+i0+j])
		}
	}
}

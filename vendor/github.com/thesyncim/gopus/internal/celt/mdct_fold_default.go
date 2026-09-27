//go:build !arm64 || purego

package celt

// The fold kernels are never called off arm64 (guarded by
// mdctUseNeonMidFold); the stubs keep the package building on all targets and
// mirror the per-element scalar sequence for reference.

func mdctFold1StoreNeon(dst []kissCpx, bitrev []int, samples []float32, window []float32, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, preScale float32) {
	for j := 0; j < 4*blocks; j++ {
		re := mdctMulAddMixEncode(samples[xp1+n2+2*j], samples[xp2-2*j], window[wp2-2*j], window[wp1+2*j])
		im := mdctMulSubMixEncode(samples[xp1+2*j], samples[xp2-n2-2*j], window[wp1+2*j], window[wp2-2*j])
		mdctStoreDirectStageFMALike(dst, bitrev[i0+j], preScale, re, im, trig[i0+j], trig[n4+i0+j])
	}
}

func mdctFold3StoreNeon(dst []kissCpx, bitrev []int, samples []float32, window []float32, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, preScale float32) {
	for j := 0; j < 4*blocks; j++ {
		re := mdctMulSubMixAlt(samples[xp2-2*j], samples[xp1-n2+2*j], window[wp2-2*j], window[wp1+2*j])
		im := mdctMulAddMixEncode(samples[xp1+2*j], samples[xp2+n2-2*j], window[wp2-2*j], window[wp1+2*j])
		mdctStoreDirectStageFMALike(dst, bitrev[i0+j], preScale, re, im, trig[i0+j], trig[n4+i0+j])
	}
}

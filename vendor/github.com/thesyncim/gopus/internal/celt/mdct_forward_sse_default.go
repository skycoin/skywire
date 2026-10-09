//go:build !amd64 || !goexperiment.simd || nosimd || purego

package celt

// mdctUseSSEForward is false off the amd64 SIMD build, which keeps the scalar
// forward MDCT rotation loops.
const mdctUseSSEForward = false

// mdctMidRotateSSE is only reached when mdctUseSSEForward is true.
func mdctMidRotateSSE(fftStage []kissCpx, bitrev []int, in, trig []float32, i0, n4, xp1, xp2, blocks int, scale float32) {
	for k := 0; k < 4*blocks; k++ {
		i := i0 + k
		mdctStoreDirectStage(fftStage, bitrev[i], scale, in[xp2-2*k], in[xp1+2*k], trig[i], trig[n4+i])
	}
}

// mdctPostTwiddleSSE is only reached when mdctUseSSEForward is true.
func mdctPostTwiddleSSE(coeffs []float32, fftStage []kissCpx, trig []float32, n2, n4, pairBlocks int) {
	mdctPostTwiddleNeon(coeffs, fftStage, trig, n2, n4, pairBlocks)
}

// mdctLeadFoldSSE is only reached when mdctUseSSEForward is true.
func mdctLeadFoldSSE(fftStage []kissCpx, bitrev []int, samples, window, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, scale float32) {
	for k := 0; k < 4*blocks; k++ {
		i := i0 + k
		re := mdctMulAddMixEncode(samples[xp1+n2+2*k], samples[xp2-2*k], window[wp2-2*k], window[wp1+2*k])
		im := mdctMulSubMixEncode(samples[xp1+2*k], samples[xp2-n2-2*k], window[wp1+2*k], window[wp2-2*k])
		mdctStoreDirectStage(fftStage, bitrev[i], scale, re, im, trig[i], trig[n4+i])
	}
}

// mdctTailFoldSSE is only reached when mdctUseSSEForward is true.
func mdctTailFoldSSE(fftStage []kissCpx, bitrev []int, samples, window, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, scale float32) {
	for k := 0; k < 4*blocks; k++ {
		i := i0 + k
		re := mdctNegMulAddMixEncode(samples[xp1-n2+2*k], samples[xp2-2*k], window[wp1+2*k], window[wp2-2*k])
		im := mdctMulAddMixEncode(samples[xp1+2*k], samples[xp2+n2-2*k], window[wp2-2*k], window[wp1+2*k])
		mdctStoreDirectStage(fftStage, bitrev[i], scale, re, im, trig[i], trig[n4+i])
	}
}

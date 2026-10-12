//go:build !arm64 || nosimd || purego || !goexperiment.simd

package celt

// mdctUsePostTwiddleNeon is false off the fused arm64 build, so the forward
// MDCT keeps its scalar post-twiddle loop and the nosimd/amd64 byte-exact
// oracle holds.
const mdctUsePostTwiddleNeon = false

// mdctPostTwiddleNeon is never called off arm64 (guarded by
// mdctUsePostTwiddleNeon); the stub keeps the package building on all
// targets.
func mdctPostTwiddleNeon(coeffs []float32, fftStage []kissCpx, trig []float32, n2, n4, pairBlocks int) {
	for i := 0; i < 4*pairBlocks; i++ {
		j := n4 - 1 - i
		coeffs[2*i] = mdctMulSubMixEncode(fftStage[i].i, fftStage[i].r, trig[n4+i], trig[i])
		coeffs[n2-1-2*i] = mdctMulAddMixEncode(fftStage[i].r, fftStage[i].i, trig[n4+i], trig[i])
		coeffs[2*j] = mdctMulSubMixEncode(fftStage[j].i, fftStage[j].r, trig[n4+j], trig[j])
		coeffs[n2-1-2*j] = mdctMulAddMixEncode(fftStage[j].r, fftStage[j].i, trig[n4+j], trig[j])
	}
}

//go:build !arm64 || purego

package celt

// mdctUsePostTwiddleNeon is false off the fused arm64 build, so the forward
// MDCT keeps its scalar post-twiddle loop and the purego/amd64 byte-exact
// oracle holds.
const mdctUsePostTwiddleNeon = false

// mdctPostTwiddleNeon is never called off arm64 (guarded by
// mdctUsePostTwiddleNeon); the stub keeps the package building on all
// targets.
func mdctPostTwiddleNeon(coeffs []float32, fftStage []kissCpx, trig []float32, n2, n4, pairBlocks int) {
	for i := 0; i < 4*pairBlocks; i++ {
		j := n4 - 1 - i
		coeffs[2*i] = mdctMul(fftStage[i].i, trig[n4+i]) - mdctMul(fftStage[i].r, trig[i])
		coeffs[n2-1-2*i] = mdctMul(fftStage[i].r, trig[n4+i]) + mdctMul(fftStage[i].i, trig[i])
		coeffs[2*j] = mdctMul(fftStage[j].i, trig[n4+j]) - mdctMul(fftStage[j].r, trig[j])
		coeffs[n2-1-2*j] = mdctMul(fftStage[j].r, trig[n4+j]) + mdctMul(fftStage[j].i, trig[j])
	}
}

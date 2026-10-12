//go:build !arm64 || nosimd || purego || !goexperiment.simd

package celt

// combUsesNeon is false off the arm64 SIMD build. The amd64 SIMD build uses
// combUsesSSE for the libopus x86 accumulation order in its scalar Go loop.
const combUsesNeon = false

// combFilterConstNeon is never called off arm64 (guarded by combUsesNeon);
// the stub keeps the package building on all targets.
func combFilterConstNeon(dst, delay []float32, g10, g11, g12 float32, blocks int) {
	for j := 0; j < 4*blocks; j++ {
		dst[j] = combFilterConstValue(dst[j], g10, g11, g12,
			delay[j+2], delay[j+3], delay[j+1], delay[j+4], delay[j])
	}
}

//go:build !arm64 || purego

package celt

// combUsesNeon is false off the fused arm64 build, so the constant-gain comb
// body keeps its scalar loops and the purego/amd64 byte-exact oracle holds.
const combUsesNeon = false

// combFilterConstNeon is never called off arm64 (guarded by combUsesNeon);
// the stub keeps the package building on all targets.
func combFilterConstNeon(dst, delay []float32, g10, g11, g12 float32, blocks int) {
	for j := 0; j < 4*blocks; j++ {
		dst[j] = combFilterConstValue(dst[j], g10, g11, g12,
			delay[j+2], delay[j+3], delay[j+1], delay[j+4], delay[j])
	}
}

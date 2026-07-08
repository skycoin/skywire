//go:build !arm64 || purego

package silk

// floatToInt16Scaled is the scalar saturate-then-round-even conversion used off
// the arm64 NEON build.
func floatToInt16Scaled(out []int16, in []float32, scale float32, n int) {
	for i := 0; i < n; i++ {
		out[i] = floatToInt16Round(in[i] * scale)
	}
}

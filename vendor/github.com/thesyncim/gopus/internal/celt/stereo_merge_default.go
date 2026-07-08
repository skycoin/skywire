//go:build !arm64 || purego

package celt

// stereoMergeRescaleNEON is the portable fallback for the arm64 rescale kernel,
// running the same per-lane noFMA32 mid/side combine over len(x) elements.
func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32) {
	n := len(x)
	for i := 0; i < n; i++ {
		l := noFMA32Mul(mid, x[i])
		r := y[i]
		x[i] = noFMA32Mul(lgain, noFMA32Sub(l, r))
		y[i] = noFMA32Mul(rgain, noFMA32Add(l, r))
	}
}

//go:build !arm64 || purego

package celt

// haar1Stride1NEON is the portable fallback for the arm64 NEON kernel. It runs
// the stride==1 Hadamard butterfly over n0 contiguous (even,odd) pairs with the
// same per-element noFMA32 ops as haar1PairNorm, keeping the purego/amd64 builds
// bit-exact with scalar libopus.
func haar1Stride1NEON(x []float32, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	idx := 0
	for j := 0; j < n0; j++ {
		tmp1 := noFMA32Mul(invSqrt2, x[idx])
		tmp2 := noFMA32Mul(invSqrt2, x[idx+1])
		x[idx] = noFMA32Add(tmp1, tmp2)
		x[idx+1] = noFMA32Sub(tmp1, tmp2)
		idx += 2
	}
}

// haar1Stride2NEON is the portable fallback for the stride==2 arm64 kernel,
// running the same two-outer-pass butterfly with idx step 4 and noFMA32 ops.
func haar1Stride2NEON(x []float32, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	const step = 4
	for i := 0; i < 2; i++ {
		idx0 := i
		idx1 := i + 2
		for j := 0; j < n0; j++ {
			tmp1 := noFMA32Mul(invSqrt2, x[idx0])
			tmp2 := noFMA32Mul(invSqrt2, x[idx1])
			x[idx0] = noFMA32Add(tmp1, tmp2)
			x[idx1] = noFMA32Sub(tmp1, tmp2)
			idx0 += step
			idx1 += step
		}
	}
}

// haar1Stride4NEON is the portable fallback for the stride==4 arm64 kernel,
// running the same four-outer-pass butterfly with idx step 8 and noFMA32 ops.
func haar1Stride4NEON(x []float32, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	const step = 8
	for i := 0; i < 4; i++ {
		idx0 := i
		idx1 := i + 4
		for j := 0; j < n0; j++ {
			tmp1 := noFMA32Mul(invSqrt2, x[idx0])
			tmp2 := noFMA32Mul(invSqrt2, x[idx1])
			x[idx0] = noFMA32Add(tmp1, tmp2)
			x[idx1] = noFMA32Sub(tmp1, tmp2)
			idx0 += step
			idx1 += step
		}
	}
}

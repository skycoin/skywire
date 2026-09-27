//go:build !arm64 || purego

package celt

// haar1Stride1NEON is the portable fallback for the arm64 NEON kernel. It runs
// the stride==1 Hadamard butterfly over n0 contiguous (even,odd) pairs with the
// same per-element noFMA32 ops as haar1PairNorm, keeping the purego/amd64 builds
// bit-exact with scalar libopus.
func haar1Stride1NEON(x []float32, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	// Caller slices x to 2*n0, so len(buf)>=2 proves both buf[0] and buf[1] are
	// in bounds — no per-pair bounds checks. Two pairs per iteration halves loop
	// overhead and exposes four independent accumulator slots to the pipeline.
	buf := x
	for len(buf) >= 4 {
		t0 := noFMA32Mul(invSqrt2, buf[0])
		t1 := noFMA32Mul(invSqrt2, buf[1])
		t2 := noFMA32Mul(invSqrt2, buf[2])
		t3 := noFMA32Mul(invSqrt2, buf[3])
		buf[0] = noFMA32Add(t0, t1)
		buf[1] = noFMA32Sub(t0, t1)
		buf[2] = noFMA32Add(t2, t3)
		buf[3] = noFMA32Sub(t2, t3)
		buf = buf[4:]
	}
	if len(buf) >= 2 {
		t0 := noFMA32Mul(invSqrt2, buf[0])
		t1 := noFMA32Mul(invSqrt2, buf[1])
		buf[0] = noFMA32Add(t0, t1)
		buf[1] = noFMA32Sub(t0, t1)
	}
}

// haar1Stride2NEON is the portable fallback for the stride==2 arm64 kernel.
// The two outer passes are fused into a single 4-element stride loop, which is
// cache-friendlier and eliminates the stride-4 counter that blocked BCE.
func haar1Stride2NEON(x []float32, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	// Each group of 4 = one iteration of the original two outer passes.
	// Caller ensures len(x) >= 4*n0 via the slice argument.
	buf := x
	for len(buf) >= 4 {
		t0 := noFMA32Mul(invSqrt2, buf[0])
		t1 := noFMA32Mul(invSqrt2, buf[1])
		t2 := noFMA32Mul(invSqrt2, buf[2])
		t3 := noFMA32Mul(invSqrt2, buf[3])
		buf[0] = noFMA32Add(t0, t2)
		buf[1] = noFMA32Add(t1, t3)
		buf[2] = noFMA32Sub(t0, t2)
		buf[3] = noFMA32Sub(t1, t3)
		buf = buf[4:]
	}
}

// haar1Stride4NEON is the portable fallback for the stride==4 arm64 kernel.
// The four outer passes are fused into a single 8-element stride loop.
func haar1Stride4NEON(x []float32, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	// Each group of 8 = one iteration of the original four outer passes.
	// Caller ensures len(x) >= 8*n0 via the slice argument.
	buf := x
	for len(buf) >= 8 {
		t0 := noFMA32Mul(invSqrt2, buf[0])
		t1 := noFMA32Mul(invSqrt2, buf[1])
		t2 := noFMA32Mul(invSqrt2, buf[2])
		t3 := noFMA32Mul(invSqrt2, buf[3])
		t4 := noFMA32Mul(invSqrt2, buf[4])
		t5 := noFMA32Mul(invSqrt2, buf[5])
		t6 := noFMA32Mul(invSqrt2, buf[6])
		t7 := noFMA32Mul(invSqrt2, buf[7])
		buf[0] = noFMA32Add(t0, t4)
		buf[1] = noFMA32Add(t1, t5)
		buf[2] = noFMA32Add(t2, t6)
		buf[3] = noFMA32Add(t3, t7)
		buf[4] = noFMA32Sub(t0, t4)
		buf[5] = noFMA32Sub(t1, t5)
		buf[6] = noFMA32Sub(t2, t6)
		buf[7] = noFMA32Sub(t3, t7)
		buf = buf[8:]
	}
}

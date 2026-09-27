//go:build !arm64 || purego

package celt

// stereoMergeRescaleNEON is the portable fallback for the arm64 rescale kernel,
// running the same per-lane noFMA32 mid/side combine over len(x) elements.
func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32) {
	n := len(x)
	if n <= 0 {
		return
	}
	// x already has len n; reslice y to n so the compiler proves both indices
	// in-bounds and drops the per-iteration bounds checks (the caller always
	// passes len(y) == len(x); a shorter y panics here, as it did at y[i]).
	x = x[:n]
	y = y[:n]
	// 8-wide loop: 8 pairs per iteration exposes 8 independent critical paths
	// (each pair is lgain*(mid*x−y) and rgain*(mid*x+y)) so throughput rather
	// than per-pair latency limits the loop, approximately halving the cycle
	// count vs the 4-wide version on wide-dispatch cores.
	i := 0
	for ; i+8 <= n; i += 8 {
		l0 := noFMA32Mul(mid, x[i])
		l1 := noFMA32Mul(mid, x[i+1])
		l2 := noFMA32Mul(mid, x[i+2])
		l3 := noFMA32Mul(mid, x[i+3])
		l4 := noFMA32Mul(mid, x[i+4])
		l5 := noFMA32Mul(mid, x[i+5])
		l6 := noFMA32Mul(mid, x[i+6])
		l7 := noFMA32Mul(mid, x[i+7])
		r0, r1, r2, r3 := y[i], y[i+1], y[i+2], y[i+3]
		r4, r5, r6, r7 := y[i+4], y[i+5], y[i+6], y[i+7]
		x[i] = noFMA32Mul(lgain, noFMA32Sub(l0, r0))
		x[i+1] = noFMA32Mul(lgain, noFMA32Sub(l1, r1))
		x[i+2] = noFMA32Mul(lgain, noFMA32Sub(l2, r2))
		x[i+3] = noFMA32Mul(lgain, noFMA32Sub(l3, r3))
		x[i+4] = noFMA32Mul(lgain, noFMA32Sub(l4, r4))
		x[i+5] = noFMA32Mul(lgain, noFMA32Sub(l5, r5))
		x[i+6] = noFMA32Mul(lgain, noFMA32Sub(l6, r6))
		x[i+7] = noFMA32Mul(lgain, noFMA32Sub(l7, r7))
		y[i] = noFMA32Mul(rgain, noFMA32Add(l0, r0))
		y[i+1] = noFMA32Mul(rgain, noFMA32Add(l1, r1))
		y[i+2] = noFMA32Mul(rgain, noFMA32Add(l2, r2))
		y[i+3] = noFMA32Mul(rgain, noFMA32Add(l3, r3))
		y[i+4] = noFMA32Mul(rgain, noFMA32Add(l4, r4))
		y[i+5] = noFMA32Mul(rgain, noFMA32Add(l5, r5))
		y[i+6] = noFMA32Mul(rgain, noFMA32Add(l6, r6))
		y[i+7] = noFMA32Mul(rgain, noFMA32Add(l7, r7))
	}
	for ; i+4 <= n; i += 4 {
		l0 := noFMA32Mul(mid, x[i])
		l1 := noFMA32Mul(mid, x[i+1])
		l2 := noFMA32Mul(mid, x[i+2])
		l3 := noFMA32Mul(mid, x[i+3])
		r0, r1, r2, r3 := y[i], y[i+1], y[i+2], y[i+3]
		x[i] = noFMA32Mul(lgain, noFMA32Sub(l0, r0))
		x[i+1] = noFMA32Mul(lgain, noFMA32Sub(l1, r1))
		x[i+2] = noFMA32Mul(lgain, noFMA32Sub(l2, r2))
		x[i+3] = noFMA32Mul(lgain, noFMA32Sub(l3, r3))
		y[i] = noFMA32Mul(rgain, noFMA32Add(l0, r0))
		y[i+1] = noFMA32Mul(rgain, noFMA32Add(l1, r1))
		y[i+2] = noFMA32Mul(rgain, noFMA32Add(l2, r2))
		y[i+3] = noFMA32Mul(rgain, noFMA32Add(l3, r3))
	}
	for ; i < n; i++ {
		l := noFMA32Mul(mid, x[i])
		r := y[i]
		x[i] = noFMA32Mul(lgain, noFMA32Sub(l, r))
		y[i] = noFMA32Mul(rgain, noFMA32Add(l, r))
	}
}

//go:build (!arm64 && !amd64) || nosimd || purego || !goexperiment.simd

package celt

// The scalar haar1 butterflies below round both products before the sum and
// difference, as haar1PairValues does without FMA; on the AMD64 v3 target
// (haar1UsesFMA) they call haar1PairValues for its contracted shape. The
// non-FMA arithmetic is written out in each group. The group loops use
// checked fixed-size array views of x.

// haar1Scale holds the haar1 butterfly scale as a variable, so the loops load
// it once into a register instead of rereading the constant every group.
var haar1Scale = [1]float32{0.7071067811865476}

// haar1Stride1 is the scalar stride==1 Hadamard butterfly over the n0
// contiguous (even, odd) pairs of x, which the caller slices to 2*n0.
func haar1Stride1(x []float32, n0 int) {
	s := haar1Scale[0]
	n := len(x) &^ 3
	for i := 0; i < n; i += 4 {
		p := (*[4]float32)(x[i : i+4])
		if haar1UsesFMA {
			p[0], p[1] = haar1PairValues(s, p[0], p[1])
			p[2], p[3] = haar1PairValues(s, p[2], p[3])
		} else {
			a0, b0 := float32(s*p[0]), float32(s*p[1])
			a1, b1 := float32(s*p[2]), float32(s*p[3])
			p[0], p[1] = a0+b0, a0-b0
			p[2], p[3] = a1+b1, a1-b1
		}
	}
	if len(x)-n >= 2 {
		p := (*[2]float32)(x[n : n+2])
		if haar1UsesFMA {
			p[0], p[1] = haar1PairValues(s, p[0], p[1])
		} else {
			a, b := float32(s*p[0]), float32(s*p[1])
			p[0], p[1] = a+b, a-b
		}
	}
}

// haar1Stride2 is the scalar stride==2 butterfly. The two outer passes are
// fused into one loop over groups of four; the caller slices x to 4*n0.
func haar1Stride2(x []float32, n0 int) {
	s := haar1Scale[0]
	n := len(x) &^ 3
	for i := 0; i < n; i += 4 {
		p := (*[4]float32)(x[i : i+4])
		if haar1UsesFMA {
			p[0], p[2] = haar1PairValues(s, p[0], p[2])
			p[1], p[3] = haar1PairValues(s, p[1], p[3])
		} else {
			a0, b0 := float32(s*p[0]), float32(s*p[2])
			a1, b1 := float32(s*p[1]), float32(s*p[3])
			p[0], p[2] = a0+b0, a0-b0
			p[1], p[3] = a1+b1, a1-b1
		}
	}
}

// haar1Stride4 is the scalar stride==4 butterfly. The four outer passes are
// fused into one loop over groups of eight; the caller slices x to 8*n0.
func haar1Stride4(x []float32, n0 int) {
	s := haar1Scale[0]
	n := len(x) &^ 7
	for i := 0; i < n; i += 8 {
		p := (*[8]float32)(x[i : i+8])
		if haar1UsesFMA {
			p[0], p[4] = haar1PairValues(s, p[0], p[4])
			p[1], p[5] = haar1PairValues(s, p[1], p[5])
			p[2], p[6] = haar1PairValues(s, p[2], p[6])
			p[3], p[7] = haar1PairValues(s, p[3], p[7])
		} else {
			a0, b0 := float32(s*p[0]), float32(s*p[4])
			a1, b1 := float32(s*p[1]), float32(s*p[5])
			a2, b2 := float32(s*p[2]), float32(s*p[6])
			a3, b3 := float32(s*p[3]), float32(s*p[7])
			p[0], p[4] = a0+b0, a0-b0
			p[1], p[5] = a1+b1, a1-b1
			p[2], p[6] = a2+b2, a2-b2
			p[3], p[7] = a3+b3, a3-b3
		}
	}
}

package celt

import "github.com/thesyncim/gopus/internal/opusmath"

// absF32 is fabsf: it clears the sign bit without a data-dependent branch.
func absF32(v float32) float32 {
	return opusmath.AbsF32(v)
}

// absSumSerial is the libopus float loop `for (i=0;i<N;i++) s += ABS(x[i])`:
// one left-to-right accumulator, which is what gcc and clang emit for it
// without -ffast-math.
func absSumSerial(x []float32) float32 {
	var s float32
	for _, v := range x {
		s += absF32(v)
	}
	return s
}

// absSumSerial2 returns absSumSerial(a) and absSumSerial(b). The two
// accumulator chains are independent, so interleaving them overlaps their
// add latencies without changing either sum. len(b) must be at least len(a).
func absSumSerial2(a, b []float32) (float32, float32) {
	b = b[:len(a)]
	var s0, s1 float32
	for i := range a {
		s0 += absF32(a[i])
		s1 += absF32(b[i])
	}
	return s0, s1
}

// absSumSerial3 is absSumSerial2 for three slices of at least len(a) elements.
func absSumSerial3(a, b, c []float32) (float32, float32, float32) {
	b, c = b[:len(a)], c[:len(a)]
	var s0, s1, s2 float32
	for i := range a {
		s0 += absF32(a[i])
		s1 += absF32(b[i])
		s2 += absF32(c[i])
	}
	return s0, s1, s2
}

// absSumSerial4 is absSumSerial2 for four slices of at least len(a) elements.
func absSumSerial4(a, b, c, d []float32) (float32, float32, float32, float32) {
	b, c, d = b[:len(a)], c[:len(a)], d[:len(a)]
	var s0, s1, s2, s3 float32
	for i := range a {
		s0 += absF32(a[i])
		s1 += absF32(b[i])
		s2 += absF32(c[i])
		s3 += absF32(d[i])
	}
	return s0, s1, s2, s3
}

// absSumSerial5 is absSumSerial2 for five slices of at least len(a) elements.
func absSumSerial5(a, b, c, d, e []float32) (float32, float32, float32, float32, float32) {
	b, c, d, e = b[:len(a)], c[:len(a)], d[:len(a)], e[:len(a)]
	var s0, s1, s2, s3, s4 float32
	for i := range a {
		s0 += absF32(a[i])
		s1 += absF32(b[i])
		s2 += absF32(c[i])
		s3 += absF32(d[i])
		s4 += absF32(e[i])
	}
	return s0, s1, s2, s3, s4
}

// absSumLevels stores in sums[l] the abs-sum of levels[l*n:(l+1)*n] for each
// l < count, with the per-level reduction l1MetricNorm uses.
func absSumLevels(levels []celtNorm, n, count int, sums []float32) {
	if celtAbsSumUsesNeon {
		for l := range count {
			sums[l] = l1AbsSumNeon(levels[l*n:(l+1)*n], n)
		}
		return
	}
	level := func(l int) []float32 { return levels[l*n : (l+1)*n] }
	l := 0
	for ; count-l >= 5; l += 5 {
		sums[l], sums[l+1], sums[l+2], sums[l+3], sums[l+4] = absSumFive(level(l), level(l+1), level(l+2), level(l+3), level(l+4))
	}
	switch count - l {
	case 4:
		sums[l], sums[l+1], sums[l+2], sums[l+3] = absSumQuad(level(l), level(l+1), level(l+2), level(l+3))
	case 3:
		sums[l], sums[l+1], sums[l+2] = absSumSerial3(level(l), level(l+1), level(l+2))
	case 2:
		sums[l], sums[l+1] = absSumPair(level(l), level(l+1))
	case 1:
		sums[l] = absSumSerial(level(l))
	}
}

// absSumSig2 returns the abs-sums of a and b with the reduction the selected
// build pairs with libopus.
func absSumSig2(a, b []celtSig) (opusVal32, opusVal32) {
	if celtAbsSumUsesNeon {
		return l1AbsSumNeon(a, len(a)), l1AbsSumNeon(b, len(b))
	}
	return absSumPair(a, b)
}

// absSumSig4 is absSumSig2 for four equal-length slices.
func absSumSig4(a, b, c, d []celtSig) (opusVal32, opusVal32, opusVal32, opusVal32) {
	if celtAbsSumUsesNeon {
		return l1AbsSumNeon(a, len(a)), l1AbsSumNeon(b, len(b)), l1AbsSumNeon(c, len(c)), l1AbsSumNeon(d, len(d))
	}
	return absSumQuad(a, b, c, d)
}

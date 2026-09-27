package celt

// CELT's float build stores these codec-domain values as C float.
// Keep internal aliases explicit so state storage matches libopus width.
type celtNorm = float32
type celtSig = float32
type celtEner = float32
type celtGLog = float32
type opusVal16 = float32
type opusVal32 = float32
type opusRes = float32

// floor32ToInt mirrors libopus float-build floor() calls while keeping the
// expression rounded to C float before converting to an integer.
func floor32ToInt(v float32) int {
	i := int(v)
	if float32(i) > v {
		i--
	}
	return i
}

// CeltEner exposes CELT's float-build celt_ener width to sibling packages that
// need to carry CELT-owned band-energy scratch without widening it.
type CeltEner = celtEner

// CeltNorm exposes CELT's float-build celt_norm width to tests and sibling
// packages that need to pass normalized CELT vectors without widening them.
type CeltNorm = celtNorm

func ensureSigSlice(buf *[]celtSig, n int) []celtSig {
	if n <= 0 {
		return nil
	}
	if cap(*buf) < n {
		*buf = make([]celtSig, n)
	} else {
		*buf = (*buf)[:n]
		clear(*buf)
	}
	return (*buf)[:n]
}

func ensureSigSliceNoClear(buf *[]celtSig, n int) []celtSig {
	if n <= 0 {
		return nil
	}
	if cap(*buf) < n {
		*buf = make([]celtSig, n)
	} else {
		*buf = (*buf)[:n]
	}
	return (*buf)[:n]
}

func absSumSig(x []celtSig) opusVal32 {
	if celtAbsSumUsesNeon {
		return l1AbsSumNeon(x, len(x))
	}
	// Eight independent accumulators: 8 ops/4 dispatch = 2 cycles per 8-element
	// block, matching the FADD latency so no stall between iterations.
	var a0, a1, a2, a3, a4, a5, a6, a7 float32
	for len(x) >= 8 {
		v0, v1, v2, v3 := x[0], x[1], x[2], x[3]
		v4, v5, v6, v7 := x[4], x[5], x[6], x[7]
		if v0 < 0 {
			v0 = -v0
		}
		if v1 < 0 {
			v1 = -v1
		}
		if v2 < 0 {
			v2 = -v2
		}
		if v3 < 0 {
			v3 = -v3
		}
		if v4 < 0 {
			v4 = -v4
		}
		if v5 < 0 {
			v5 = -v5
		}
		if v6 < 0 {
			v6 = -v6
		}
		if v7 < 0 {
			v7 = -v7
		}
		a0 += v0
		a1 += v1
		a2 += v2
		a3 += v3
		a4 += v4
		a5 += v5
		a6 += v6
		a7 += v7
		x = x[8:]
	}
	for _, v := range x {
		if v < 0 {
			v = -v
		}
		a0 += v
	}
	return opusVal32((a0 + a1 + a2 + a3) + (a4 + a5 + a6 + a7))
}

func interleaveSigToFloat32(left, right []celtSig, dst []float32) {
	n := min(len(left), len(right))
	n = min(n, len(dst)/2)
	for i := 0; i < n; i++ {
		dst[2*i] = float32(left[i])
		dst[2*i+1] = float32(right[i])
	}
}

func copyFloat32ToSig(dst []celtSig, src []float32) {
	// celtSig is float32, so this is a plain element copy; copy() lowers to a
	// SIMD-optimized memmove instead of a scalar per-element loop.
	copy(dst, src)
}

func copySigToFloat32(dst []float32, src []celtSig) {
	// celtSig is float32; copy() is the memmove-backed equivalent of the cast.
	copy(dst, src)
}

func ensureNormSlice(buf *[]celtNorm, n int) []celtNorm {
	if n <= 0 {
		return nil
	}
	if cap(*buf) < n {
		*buf = make([]celtNorm, n)
	} else {
		*buf = (*buf)[:n]
		clear(*buf)
	}
	return (*buf)[:n]
}

func ensureNormSliceNoClear(buf *[]celtNorm, n int) []celtNorm {
	if n <= 0 {
		return nil
	}
	if cap(*buf) < n {
		*buf = make([]celtNorm, n)
	} else {
		*buf = (*buf)[:n]
	}
	return (*buf)[:n]
}

func ensureEnerSlice(buf *[]celtEner, n int) []celtEner {
	if n <= 0 {
		return nil
	}
	if cap(*buf) < n {
		*buf = make([]celtEner, n)
	} else {
		*buf = (*buf)[:n]
		clear(*buf)
	}
	return (*buf)[:n]
}

func ensureGLogSlice(buf *[]celtGLog, n int) []celtGLog {
	if n <= 0 {
		return nil
	}
	if cap(*buf) < n {
		*buf = make([]celtGLog, n)
	} else {
		*buf = (*buf)[:n]
		clear(*buf)
	}
	return (*buf)[:n]
}

func ensureGLogSliceNoClear(buf *[]celtGLog, n int) []celtGLog {
	if n <= 0 {
		return nil
	}
	if cap(*buf) < n {
		*buf = make([]celtGLog, n)
	} else {
		*buf = (*buf)[:n]
	}
	return (*buf)[:n]
}

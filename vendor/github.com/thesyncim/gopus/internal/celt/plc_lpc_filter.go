package celt

func xcorrKernel4Float32(x, y []float32, sum *[4]float32, length int) {
	if length <= 0 {
		return
	}
	// The kernel reads x[0:length] and y[0:length+3]. Slicing to those exact
	// bounds and advancing the slices (rather than indexing off a stride-4
	// counter, which prove cannot reason about) eliminates every per-access
	// bounds check in the unrolled body. The walk and the FP expressions are
	// unchanged, so results stay bit-identical per arch.
	x = x[:length]
	y = y[:length+3]
	// Accumulate in registers: with sum as a pointer every sum[k]+= is a
	// memory read-modify-write the compiler cannot keep in a register. Locals
	// run the identical IEEE adds in the identical order, written back once.
	s0, s1, s2, s3 := sum[0], sum[1], sum[2], sum[3]
	var y3 float32
	y0 := y[0]
	y1 := y[1]
	y2 := y[2]
	for len(x) >= 4 && len(y) >= 7 {
		tmp := x[0]
		y3 = y[3]
		s0 += tmp * y0
		s1 += tmp * y1
		s2 += tmp * y2
		s3 += tmp * y3

		tmp = x[1]
		y0 = y[4]
		s0 += tmp * y1
		s1 += tmp * y2
		s2 += tmp * y3
		s3 += tmp * y0

		tmp = x[2]
		y1 = y[5]
		s0 += tmp * y2
		s1 += tmp * y3
		s2 += tmp * y0
		s3 += tmp * y1

		tmp = x[3]
		y2 = y[6]
		s0 += tmp * y3
		s1 += tmp * y0
		s2 += tmp * y1
		s3 += tmp * y2

		x = x[4:]
		y = y[4:]
	}
	if len(x) >= 1 && len(y) >= 4 {
		tmp := x[0]
		y3 = y[3]
		s0 += tmp * y0
		s1 += tmp * y1
		s2 += tmp * y2
		s3 += tmp * y3
		x = x[1:]
		y = y[1:]
	}
	if len(x) >= 1 && len(y) >= 4 {
		tmp := x[0]
		y0 = y[3]
		s0 += tmp * y1
		s1 += tmp * y2
		s2 += tmp * y3
		s3 += tmp * y0
		x = x[1:]
		y = y[1:]
	}
	if len(x) >= 1 && len(y) >= 4 {
		tmp := x[0]
		y1 = y[3]
		s0 += tmp * y2
		s1 += tmp * y3
		s2 += tmp * y0
		s3 += tmp * y1
	}
	sum[0] = s0
	sum[1] = s1
	sum[2] = s2
	sum[3] = s3
}

// xcorrKernel8Float32 computes eight simultaneous cross-correlation lags in a
// single pass over x, reading x[0:length] and y[0:length+7].
//
// Each lag uses two independent sub-accumulators ('a' for even x-samples,
// 'b' for odd), giving 16 independent chains total. The ordering ensures that
// when x2 re-uses the 'a' chains, their FADD from x0 has already retired
// (8 other ops ≈ 2–4 cycles elapsed, FADD latency ≈ 3 cycles on M-series),
// so the inner loop runs at peak dispatch rate with no stalls.
func xcorrKernel8Float32(x, y []float32, sum *[8]float32, length int) {
	if length <= 0 {
		return
	}
	x = x[:length]
	y = y[:length+7]
	// 'a' sub-accumulators receive x0 and x2; 'b' receive x1 and x3.
	var s0a, s1a, s2a, s3a, s4a, s5a, s6a, s7a float32
	var s0b, s1b, s2b, s3b, s4b, s5b, s6b, s7b float32
	s0a, s1a, s2a, s3a = sum[0], sum[1], sum[2], sum[3]
	s4a, s5a, s6a, s7a = sum[4], sum[5], sum[6], sum[7]
	for len(x) >= 4 && len(y) >= 11 {
		x0, x1, x2, x3 := x[0], x[1], x[2], x[3]
		y0, y1, y2, y3, y4, y5, y6, y7, y8, y9, y10 := y[0], y[1], y[2], y[3], y[4], y[5], y[6], y[7], y[8], y[9], y[10]

		// x0 → 'a' chains (8 independent FMAs, issued in 2 cycles)
		s0a += x0 * y0
		s1a += x0 * y1
		s2a += x0 * y2
		s3a += x0 * y3
		s4a += x0 * y4
		s5a += x0 * y5
		s6a += x0 * y6
		s7a += x0 * y7

		// x1 → 'b' chains (8 independent FMAs, cycles 2–3; 'a' not needed yet)
		s0b += x1 * y1
		s1b += x1 * y2
		s2b += x1 * y3
		s3b += x1 * y4
		s4b += x1 * y5
		s5b += x1 * y6
		s6b += x1 * y7
		s7b += x1 * y8

		// x2 → 'a' chains (cycles 4–5; 'a' from x0 finished at cycle 3 ✓)
		s0a += x2 * y2
		s1a += x2 * y3
		s2a += x2 * y4
		s3a += x2 * y5
		s4a += x2 * y6
		s5a += x2 * y7
		s6a += x2 * y8
		s7a += x2 * y9

		// x3 → 'b' chains (cycles 6–7; 'b' from x1 finished at cycle 5 ✓)
		s0b += x3 * y3
		s1b += x3 * y4
		s2b += x3 * y5
		s3b += x3 * y6
		s4b += x3 * y7
		s5b += x3 * y8
		s6b += x3 * y9
		s7b += x3 * y10

		x = x[4:]
		y = y[4:]
	}
	for len(x) >= 1 && len(y) >= 8 {
		t := x[0]
		s0a += t * y[0]
		s1a += t * y[1]
		s2a += t * y[2]
		s3a += t * y[3]
		s4a += t * y[4]
		s5a += t * y[5]
		s6a += t * y[6]
		s7a += t * y[7]
		x = x[1:]
		y = y[1:]
	}
	sum[0] = s0a + s0b
	sum[1] = s1a + s1b
	sum[2] = s2a + s2b
	sum[3] = s3a + s3b
	sum[4] = s4a + s4b
	sum[5] = s5a + s5b
	sum[6] = s6a + s6b
	sum[7] = s7a + s7b
}

func celtFIRFloat32(dst []celtSig, exc []celtSig, start, length int, lpc []float32) {
	const ord = celtPLCLPCOrder
	if length <= 0 || len(dst) < length || start-ord < 0 || start+length > len(exc) || len(lpc) < ord {
		return
	}
	var rnum [ord]float32
	for i := range ord {
		rnum[i] = float32(lpc[ord-1-i])
	}
	i := 0
	for ; i < length-3; i += 4 {
		sum := [4]float32{
			exc[start+i],
			exc[start+i+1],
			exc[start+i+2],
			exc[start+i+3],
		}
		xcorrKernel4Float32(rnum[:], exc[start+i-ord:], &sum, ord)
		dst[i] = celtSig(sum[0])
		dst[i+1] = celtSig(sum[1])
		dst[i+2] = celtSig(sum[2])
		dst[i+3] = celtSig(sum[3])
	}
	for ; i < length; i++ {
		sum := float32(exc[start+i])
		for j := range ord {
			sum += rnum[j] * exc[start+i+j-ord]
		}
		dst[i] = celtSig(sum)
	}
}

func (d *Decoder) celtIIRFloat32(dst []celtSig, hist []celtSig, lpc []float32, length int) {
	const ord = celtPLCLPCOrder
	if length <= 0 || len(dst) < length || len(hist) < plcDecodeBufferSize || len(lpc) < ord {
		return
	}
	var rden [ord]float32
	var mem [ord]float32
	for i := range ord {
		rden[i] = float32(lpc[ord-1-i])
		mem[i] = float32(hist[plcDecodeBufferSize-1-i])
	}
	y := ensureFloat32Slice(&d.scratchPLCIIRY, length+ord)
	for i := range ord {
		y[i] = -mem[ord-i-1]
	}
	clear(y[ord:])

	i := 0
	for ; i < length-3; i += 4 {
		sum := [4]float32{
			float32(dst[i]),
			float32(dst[i+1]),
			float32(dst[i+2]),
			float32(dst[i+3]),
		}
		xcorrKernel4Float32(rden[:], y[i:], &sum, ord)

		y[i+ord] = -sum[0]
		dst[i] = celtSig(sum[0])

		sum[1] += y[i+ord] * float32(lpc[0])
		y[i+ord+1] = -sum[1]
		dst[i+1] = celtSig(sum[1])

		sum[2] += y[i+ord+1] * float32(lpc[0])
		sum[2] += y[i+ord] * float32(lpc[1])
		y[i+ord+2] = -sum[2]
		dst[i+2] = celtSig(sum[2])

		sum[3] += y[i+ord+2] * float32(lpc[0])
		sum[3] += y[i+ord+1] * float32(lpc[1])
		sum[3] += y[i+ord] * float32(lpc[2])
		y[i+ord+3] = -sum[3]
		dst[i+3] = celtSig(sum[3])
	}
	for ; i < length; i++ {
		sum := float32(dst[i])
		for j := range ord {
			sum -= rden[j] * y[i+j]
		}
		y[i+ord] = sum
		dst[i] = celtSig(sum)
	}
}

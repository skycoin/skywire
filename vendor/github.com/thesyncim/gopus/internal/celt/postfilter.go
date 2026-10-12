package celt

import "github.com/thesyncim/gopus/internal/extsupport"

const (
	combFilterMinPeriod = 15
	combFilterMaxPeriod = 1024
	combFilterHistory   = combFilterMaxPeriod + 2
	// Matches libopus DEC_PITCH_BUF_SIZE used by celt_decode_lost().
	plcDecodeBufferSize = 2048
)

func (d *Decoder) qextDecodeScale() int {
	if extsupport.QEXT && d.sampleRate == 96000 {
		if d.customScaleBase == 180 || d.customScaleBase == 240 || d.customScaleBase == 0 && d.synthOverlap == 240 {
			return 2
		}
	}
	return 1
}

func (d *Decoder) plcDecodeBufferLen() int {
	return plcDecodeBufferSize * d.qextDecodeScale()
}

func (d *Decoder) plcCombFilterMaxPeriod() int {
	return combFilterMaxPeriod * d.qextDecodeScale()
}

func (d *Decoder) plcCombFilterMinPeriod() int {
	return combFilterMinPeriod * d.qextDecodeScale()
}

func (d *Decoder) plcCombFilterHistoryLen() int {
	return combFilterMaxPeriod*d.qextDecodeScale() + 2
}

var combFilterGains = [3][3]float32{
	{0.3066406250, 0.2170410156, 0.1296386719},
	{0.4638671875, 0.2680664062, 0.0000000000},
	{0.7998046875, 0.1000976562, 0.0000000000},
}

func combGain32(g float32, tapset, tap int) float32 {
	return g * combFilterGains[tapset][tap]
}

func (d *Decoder) clampDecodePostfilterPeriods() {
	if d.postfilterPeriod < combFilterMinPeriod {
		d.postfilterPeriod = combFilterMinPeriod
	}
	if d.postfilterPeriodOld < combFilterMinPeriod {
		d.postfilterPeriodOld = combFilterMinPeriod
	}
}

func sanitizePostfilterParams(t0, t1 int, g0, g1 float32, tap0, tap1 int) (int, int, int, int) {
	if t0 < combFilterMinPeriod || t0 > combFilterMaxPeriod {
		t0 = t1
	}
	if t1 < combFilterMinPeriod || t1 > combFilterMaxPeriod {
		t1 = t0
	}
	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
	}

	if tap0 < 0 || tap0 >= len(combFilterGains) {
		tap0 = tap1
	}
	if tap1 < 0 || tap1 >= len(combFilterGains) {
		tap1 = tap0
	}
	if tap0 < 0 || tap0 >= len(combFilterGains) {
		tap0 = 0
	}
	if tap1 < 0 || tap1 >= len(combFilterGains) {
		tap1 = 0
	}

	if g0 == 0 {
		t0 = t1
	}
	if g1 == 0 {
		t1 = t0
	}

	return t0, t1, tap0, tap1
}

func postfilterHistoryNeed(t0, t1, t1b, t2 int) int {
	need := max(t1b, max(t1, t0))
	if t2 > need {
		need = t2
	}
	need += 2
	if need > combFilterHistory {
		return combFilterHistory
	}
	if need < 0 {
		return 0
	}
	return need
}

// postfilterWindowSquareF32 returns the squared mode window the comb-filter
// cross-fade weights with (celt/celt.c comb_filter's window[i]*window[i]).
// The squares depend only on the window, so they are computed once per window
// and kept until the window or a reset changes them.
func (d *Decoder) postfilterWindowSquareF32(overlap int) []float32 {
	window := d.scratchIMDCTF32.modeWindow(overlap)
	if len(window) == 0 {
		return nil
	}
	if d.postfilterWindowSqOf == &window[0] && len(d.postfilterWindowSqF32) == len(window) {
		return d.postfilterWindowSqF32
	}
	windowSq := ensureFloat32Slice(&d.postfilterWindowSqF32, len(window))
	for i, w := range window {
		windowSq[i] = noFMA32Mul(w, w)
	}
	d.postfilterWindowSqOf = &window[0]
	return windowSq
}

// combFilterOverlapScalar is the scalar form of combFilterOverlap.
func combFilterOverlapScalar(dst, d0, d1, wsq []float32, g00, g01, g02, g10, g11, g12 float32) {
	n := len(dst)
	d0 = d0[: n+4 : n+4]
	d1 = d1[: n+4 : n+4]
	wsq = wsq[:n]
	for i := range dst {
		// The five taps of output i, resliced once so the tap loads need no
		// bounds checks.
		t0 := d0[i : i+5 : i+5]
		t1 := d1[i : i+5 : i+5]
		f := wsq[i]
		oneMinus := float32(1.0) - f
		if combTargetV3FMA {
			// GCC contracts the six tap products into the running sum for the
			// x86-64-v3 libopus scalar overlap loop. The cross-fade weights,
			// tap-pair sums, and per-tap coefficients remain separately rounded.
			c00 := noFMA32Mul(oneMinus, g00)
			c01 := noFMA32Mul(oneMinus, g01)
			c02 := noFMA32Mul(oneMinus, g02)
			c10 := noFMA32Mul(f, g10)
			c11 := noFMA32Mul(f, g11)
			c12 := noFMA32Mul(f, g12)
			p01 := noFMA32Add(t0[3], t0[1])
			p02 := noFMA32Add(t0[4], t0[0])
			p11 := noFMA32Add(t1[3], t1[1])
			p12 := noFMA32Add(t1[4], t1[0])
			dst[i] = combFilterOverlapV3Accumulate(dst[i], c00, t0[2], c01, p01, c02, p02, c10, t1[2], c11, p11, c12, p12)
			continue
		}
		dst[i] = dst[i] +
			(oneMinus*g00)*t0[2] +
			(oneMinus*g01)*(t0[3]+t0[1]) +
			(oneMinus*g02)*(t0[4]+t0[0]) +
			(f*g10)*t1[2] +
			(f*g11)*(t1[3]+t1[1]) +
			(f*g12)*(t1[4]+t1[0])
	}
}

//go:noinline
func combFilterOverlapV3Accumulate(base, c00, t00, c01, t01, c02, t02, c10, t10, c11, t11, c12, t12 float32) float32 {
	value := fma32(c00, t00, base)
	value = fma32(c01, t01, value)
	value = fma32(c02, t02, value)
	value = fma32(c10, t10, value)
	value = fma32(c11, t11, value)
	return fma32(c12, t12, value)
}

// combFilterConstSSEValue matches libopus celt/x86/pitch_sse.c:
// comb_filter_const_sse() groups the outer tap products before the final add.
// The AMD64 v3 C build contracts the center product and the outer side product.
func combFilterConstSSEValue(base, g10, g11, g12, center, plus1, minus1, plus2, minus2 float32) float32 {
	main := add32(base, mul32(g10, center))
	sides := add32(mul32(g11, add32(minus1, plus1)), mul32(g12, add32(plus2, minus2)))
	if combTargetV3FMA {
		main = fma32(g10, center, base)
		sides = fma32(g12, add32(plus2, minus2), mul32(g11, add32(minus1, plus1)))
	}
	return add32(main, sides)
}

// combFilterConstDispatch runs the constant-gain comb body, handing whole
// 4-wide blocks to the NEON kernel on the fused arm64 build (bit-identical
// per element). A scalar head keeps the incoming carry semantics, and the
// carries reload from the delay line afterwards.
func combFilterConstDispatch(dst, delay []float32, g10, g11, g12 float32, x4, x3, x2, x1 float32) (float32, float32, float32, float32, bool) {
	n := len(dst)
	if !combUsesNeon || n < 9 {
		return x4, x3, x2, x1, false
	}
	delay = delay[:n:n]
	i := 0
	for ; i < 4; i++ {
		x0 := delay[i]
		dst[i] = combFilterConstValue(dst[i], g10, g11, g12, x2, x1, x3, x0, x4)
		x4, x3, x2, x1 = x3, x2, x1, x0
	}
	blocks := (n - i) >> 2
	combFilterConstNeon(dst[i:], delay[i-4:], g10, g11, g12, blocks)
	i += blocks * 4
	x1 = delay[i-1]
	x2 = delay[i-2]
	x3 = delay[i-3]
	x4 = delay[i-4]
	for ; i < n; i++ {
		x0 := delay[i]
		dst[i] = combFilterConstValue(dst[i], g10, g11, g12, x2, x1, x3, x0, x4)
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
	return x4, x3, x2, x1, true
}

// combFilterConstFloat32 is the constant-gain part of libopus comb_filter,
// dst[i] += g10*x[i-T] + g11*(x[i-T+1]+x[i-T-1]) + g12*(x[i-T+2]+x[i-T-2]),
// where delay[i] is x[i-T+2] and x4..x1 carry x[-T-2..-T+1]. The first
// sseCount outputs use the comb_filter_const_sse operation order; with the
// amd64 SIMD kernel, whole blocks of four of them run as vectors, which
// computes the same per-output expression. It returns the updated carries.
func combFilterConstFloat32(dst, delay []float32, g10, g11, g12 float32, x4, x3, x2, x1 float32, sseCount int) (float32, float32, float32, float32) {
	n := len(dst)
	if n == 0 {
		return x4, x3, x2, x1
	}
	if a4, a3, a2, a1, ok := combFilterConstDispatch(dst, delay, g10, g11, g12, x4, x3, x2, x1); ok {
		return a4, a3, a2, a1
	}
	delay = delay[:n:n]
	_ = dst[n-1]
	_ = delay[n-1]
	if combUsesSSE {
		i := 0
		for head := min(sseCount, 4); i < head; i++ {
			x0 := delay[i]
			dst[i] = combFilterConstSSEValue(dst[i], g10, g11, g12, x2, x1, x3, x0, x4)
			x4, x3, x2, x1 = x3, x2, x1, x0
		}
		if blocks := (sseCount - i) &^ 3; blocks > 0 && i == 4 {
			// delay[i-4+k] is x[i+k-T-2], the kernel's delay line.
			combFilterConstSSE(dst[i:i+blocks], dst[i:i+blocks], delay[i-4:i+blocks], 0, blocks, g10, g11, g12)
			i += blocks
			x4, x3, x2, x1 = delay[i-4], delay[i-3], delay[i-2], delay[i-1]
		}
		for ; i < sseCount; i++ {
			x0 := delay[i]
			dst[i] = combFilterConstSSEValue(dst[i], g10, g11, g12, x2, x1, x3, x0, x4)
			x4, x3, x2, x1 = x3, x2, x1, x0
		}
		for ; i < n; i++ {
			x0 := delay[i]
			dst[i] = combFilterConstValue(dst[i], g10, g11, g12, x2, x1, x3, x0, x4)
			x4, x3, x2, x1 = x3, x2, x1, x0
		}
		return x4, x3, x2, x1
	}
	i := 0
	for ; i+4 < n; i += 5 {
		x0 := delay[i]
		dst[i] = combFilterConstValue(dst[i], g10, g11, g12, x2, x1, x3, x0, x4)

		x4 = delay[i+1]
		dst[i+1] = combFilterConstValue(dst[i+1], g10, g11, g12, x1, x0, x2, x4, x3)

		x3 = delay[i+2]
		dst[i+2] = combFilterConstValue(dst[i+2], g10, g11, g12, x0, x4, x1, x3, x2)

		x2 = delay[i+3]
		dst[i+3] = combFilterConstValue(dst[i+3], g10, g11, g12, x4, x3, x0, x2, x1)

		x1 = delay[i+4]
		dst[i+4] = combFilterConstValue(dst[i+4], g10, g11, g12, x3, x2, x4, x1, x0)
	}
	for ; i < n; i++ {
		x0 := delay[i]
		dst[i] = combFilterConstValue(dst[i], g10, g11, g12, x2, x1, x3, x0, x4)
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
	return x4, x3, x2, x1
}

func combFilterWithInputSig(dst, src []celtSig, start int, t0, t1, n int, g0, g1 float32, tapset0, tapset1 int, window []float32, overlap int) {
	if n <= 0 {
		return
	}
	// Native 96 kHz HD mode: comb_filter dispatches to comb_filter_qext when
	// overlap==240 (libopus celt.c). The window is non-nil only on the
	// pitch-transition segment; the segment-0 (offset) call passes window==nil,
	// overlap==0, but libopus still routes it through comb_filter_qext.
	if extsupport.QEXT && overlap == 240 {
		combFilterWithInputSigQEXT(dst, src, start, t0, t1, n, g0, g1, tapset0, tapset1, window, overlap)
		return
	}
	if g0 == 0 && g1 == 0 {
		copy(dst[start:start+n], src[start:start+n])
		return
	}

	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
	}

	if window == nil {
		overlap = 0
	}
	if overlap > n {
		overlap = n
	}
	if window != nil && overlap > len(window) {
		overlap = len(window)
	}

	if tapset0 < 0 || tapset0 >= len(combFilterGains) {
		tapset0 = 0
	}
	if tapset1 < 0 || tapset1 >= len(combFilterGains) {
		tapset1 = 0
	}

	g00 := combGain32(g0, tapset0, 0)
	g01 := combGain32(g0, tapset0, 1)
	g02 := combGain32(g0, tapset0, 2)
	g10 := combGain32(g1, tapset1, 0)
	g11 := combGain32(g1, tapset1, 1)
	g12 := combGain32(g1, tapset1, 2)

	srcFrame := src[start:]
	dstFrame := dst[start:]
	delay1 := src[start-t1-2:]
	var delay0 []celtSig

	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	} else if overlap > 0 {
		delay0 = src[start-t0-2:]
	}

	// Reading delay1[i..i+4] directly per iteration instead of carrying
	// a shift register removes 4 serial moves and lets the compiler reorder
	// the FP work freely. The loop is short (~overlap) so unrolling is
	// unnecessary; ILP comes from the 6 independent FMUL chains in `sum`.
	// The five taps of each output are read through one five-element window of
	// delay0 and delay1, so each iteration checks bounds once per delay line.
	i := 0
	if overlap > 0 && combOverlapVector && overlap <= combOverlapMax {
		// The vector cross-fade runs in place on dst, reading the delayed
		// taps from src, so dst starts as a copy of the input.
		var wsqBuf [combOverlapMax]float32
		wsq := wsqBuf[:overlap]
		for j, w := range window[:overlap] {
			wsq[j] = noFMA32Mul(w, w)
		}
		dstO := dstFrame[:overlap]
		copy(dstO, srcFrame[:overlap])
		combFilterOverlap(dstO, delay0[:overlap+4], delay1[:overlap+4], wsq, g00, g01, g02, g10, g11, g12)
		i = overlap
	} else if overlap > 0 {
		srcO := srcFrame[:overlap]
		dstO := dstFrame[:len(srcO)]
		win := window[:len(srcO)]
		d0 := delay0[:len(srcO)+4]
		d1 := delay1[:len(srcO)+4]
		for j, x := range srcO {
			w := win[j]
			f := noFMA32Mul(w, w)
			oneMinus := float32(1.0) - f
			t0 := d0[j : j+5 : j+5]
			t1 := d1[j : j+5 : j+5]
			sum := float32(x) +
				(oneMinus*g00)*float32(t0[2]) +
				(oneMinus*g01)*(float32(t0[3])+float32(t0[1])) +
				(oneMinus*g02)*(float32(t0[4])+float32(t0[0])) +
				(f*g10)*float32(t1[2]) +
				(f*g11)*(float32(t1[3])+float32(t1[1])) +
				(f*g12)*(float32(t1[4])+float32(t1[0]))
			dstO[j] = celtSig(sum)
		}
		i = overlap
	}

	if g1 == 0 {
		if i < n {
			copy(dstFrame[i:n], srcFrame[i:n])
		}
		return
	}

	// For each position i the args are fixed offsets into delay1:
	// center=delay1[i+2], plus1=delay1[i+3], minus1=delay1[i+1],
	// plus2=delay1[i+4], minus2=delay1[i]. Reading directly (no shift
	// register) removes 4 serial moves per iteration and exposes 4-wide
	// ILP when the loop is unrolled.
	_ = delay1[n+4-1] // BCE hint
	_ = srcFrame[n-1] // BCE hint
	_ = dstFrame[n-1] // BCE hint
	if combUsesSSE {
		// libopus x86 builds that presume SSE bind comb_filter_const to
		// comb_filter_const_sse, which sums the two side taps before adding
		// them to the center term.
		full := i + (n-i)&^3
		combFilterConstSSE(dstFrame, srcFrame, delay1, i, full, g10, g11, g12)
		i = full
	}
	for ; i+3 < n; i += 4 {
		d0, d1 := float32(delay1[i]), float32(delay1[i+1])
		d2, d3 := float32(delay1[i+2]), float32(delay1[i+3])
		d4, d5 := float32(delay1[i+4]), float32(delay1[i+5])
		d6, d7 := float32(delay1[i+6]), float32(delay1[i+7])
		dstFrame[i] = celtSig(combFilterConstValue(float32(srcFrame[i]), g10, g11, g12, d2, d3, d1, d4, d0))
		dstFrame[i+1] = celtSig(combFilterConstValue(float32(srcFrame[i+1]), g10, g11, g12, d3, d4, d2, d5, d1))
		dstFrame[i+2] = celtSig(combFilterConstValue(float32(srcFrame[i+2]), g10, g11, g12, d4, d5, d3, d6, d2))
		dstFrame[i+3] = celtSig(combFilterConstValue(float32(srcFrame[i+3]), g10, g11, g12, d5, d6, d4, d7, d3))
	}
	for ; i < n; i++ {
		dstFrame[i] = celtSig(combFilterConstValue(float32(srcFrame[i]), g10, g11, g12,
			float32(delay1[i+2]), float32(delay1[i+3]), float32(delay1[i+1]), float32(delay1[i+4]), float32(delay1[i])))
	}
}

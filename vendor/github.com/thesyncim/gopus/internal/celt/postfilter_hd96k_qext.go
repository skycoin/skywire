//go:build gopus_qext

package celt

// Native 96 kHz (HD / QEXT) comb-filter postfilter.
//
// At 96 kHz libopus does not run the plain comb_filter: when mode->overlap==240
// it dispatches to comb_filter_qext (celt/celt.c). That routine splits the
// signal into its even and odd sample phases, builds a half-rate window
// (new_window[i] = window[2*i+s]), and runs the ordinary comb filter on each
// phase independently with the SAME pitch period at N/2 and overlap/2 = 120.
// This is equivalent to doubling the comb period and tap spacing (mirroring the
// filter around 24 kHz). Each phase reads up to 2*COMBFILTER_MAXPERIOD samples
// of decode_mem history.
//
// This is intentionally separate from the 48 kHz comb-filter path so that path
// stays byte-identical.

// hd96kCombHistory is 2*COMBFILTER_MAXPERIOD, the maximum history the qext comb
// filter reaches back into (libopus x[2*i+s - 2*COMBFILTER_MAXPERIOD]).
const hd96kCombHistory = 2 * combFilterMaxPeriod

// hd96kPostfilterActive reports whether the native 96 kHz comb-filter postfilter
// applies (the decoder is in HD96k mode).
func (d *Decoder) hd96kPostfilterActive() bool {
	return d.synthOverlap == 240
}

// hd96kPostfilterDecodeMem runs celt_decode_with_ec()'s two comb_filter
// calls, dispatched to comb_filter_qext, in place on every channel's out_syn of
// n samples. The even/odd phases read up to 2*COMBFILTER_MAXPERIOD samples of
// decode_mem history before out_syn.
func (d *Decoder) hd96kPostfilterDecodeMem(n, lm int, newPeriod int, newGain float32, newTapset int) {
	d.clampDecodePostfilterPeriods()
	g0, g1 := d.postfilterGainOld, d.postfilterGain
	t0, t1, tap0, tap1 := sanitizePostfilterParams(int(d.postfilterPeriodOld), int(d.postfilterPeriod), g0, g1, int(d.postfilterTapsetOld), int(d.postfilterTapset))
	t1b, t2, tap1b, tap2 := sanitizePostfilterParams(t1, newPeriod, g1, newGain, tap1, newTapset)

	overlap := d.synthOverlapLen()
	window := GetWindowBufferF32(overlap)
	shortMdctSize := n >> uint(lm)
	if shortMdctSize <= 0 || shortMdctSize > n {
		shortMdctSize = n
	}
	phase := &d.ensureQEXTState().hd96kPostPhase
	start := d.decodeMemHistoryLen() - n
	for c := range int(d.channels) {
		x := d.decodeMemChannel(c)
		combFilterQEXTFloat32(x, start, t0, t1, shortMdctSize, g0, g1, tap0, tap1, window, overlap, phase)
		if lm != 0 && shortMdctSize < n {
			combFilterQEXTFloat32(x, start+shortMdctSize, t1b, t2, n-shortMdctSize, g1, newGain, tap1b, tap2, window, overlap, phase)
		}
	}
	d.commitPostfilterState(lm, newPeriod, newGain, newTapset)
}

// combFilterQEXTFloat32 applies libopus comb_filter_qext in place over tl at
// [pos, pos+n): it deinterleaves the timeline (decode_mem, which extends at
// least 2*MAXPERIOD samples before pos) into even/odd phases, runs the plain comb filter on each phase at
// n/2 with overlap/2 and a half-rate window, and re-interleaves the result.
func combFilterQEXTFloat32(tl []float32, pos, t0, t1, n int, g0, g1 float32, tapset0, tapset1 int, window []float32, overlap int, scratch *hd96kCombPhase) {
	if n <= 0 {
		return
	}
	if g0 == 0 && g1 == 0 {
		return
	}
	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
	}
	n2 := n / 2
	overlap2 := overlap / 2
	if n2 <= 0 {
		return
	}
	newWindow := ensureFloat32Slice(&scratch.window, overlap2)
	// phase buffer: COMBFILTER_MAXPERIOD history + n2 samples.
	phase := ensureFloat32Slice(&scratch.phase, combFilterMaxPeriod+n2)

	for s := 0; s < 2; s++ {
		for i := 0; i < overlap2; i++ {
			newWindow[i] = window[2*i+s]
		}
		// mem_buf[i] = x[2*i+s - 2*MAXPERIOD], x indexed from pos.
		for i := 0; i < combFilterMaxPeriod+n2; i++ {
			srcIdx := pos + 2*i + s - hd96kCombHistory
			phase[i] = tl[srcIdx]
		}
		combFilterScalarFloat32(phase, combFilterMaxPeriod, t0, t1, n2, g0, g1, tapset0, tapset1, newWindow, overlap2)
		for i := 0; i < n2; i++ {
			tl[pos+2*i+s] = phase[combFilterMaxPeriod+i]
		}
	}
}

// combFilterScalarFloat32 is a direct transliteration of libopus comb_filter
// (the float, non-qext core) operating on a single contiguous buffer where buf
// holds `history` samples of context before the `n` samples to be filtered in
// place starting at offset `history`.
func combFilterScalarFloat32(buf []float32, history, t0, t1, n int, g0, g1 float32, tapset0, tapset1 int, window []float32, overlap int) {
	if n <= 0 {
		return
	}
	if g0 == 0 && g1 == 0 {
		return
	}
	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
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

	// x is buf shifted so x[k] == buf[history+k]; negative k reads history.
	x := func(k int) float32 { return buf[history+k] }
	y := func(k int) *float32 { return &buf[history+k] }

	if overlap > len(window) {
		overlap = len(window)
	}
	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	}

	x1 := x(-t1 + 1)
	x2 := x(-t1)
	x3 := x(-t1 - 1)
	x4 := x(-t1 - 2)

	i := 0
	for ; i < overlap; i++ {
		x0 := x(i - t1 + 2)
		f := noFMA32Mul(window[i], window[i])
		oneMinus := noFMA32Sub(1, f)
		// celt.c comb_filter rounds each interpolated gain, then the
		// selected ARM libopus kernel accumulates six taps with FMADD.
		t := x(i)
		t = fma32(noFMA32Mul(oneMinus, g00), x(i-t0), t)
		t = fma32(noFMA32Mul(oneMinus, g01), noFMA32Add(x(i-t0+1), x(i-t0-1)), t)
		t = fma32(noFMA32Mul(oneMinus, g02), noFMA32Add(x(i-t0+2), x(i-t0-2)), t)
		t = fma32(noFMA32Mul(f, g10), x2, t)
		t = fma32(noFMA32Mul(f, g11), noFMA32Add(x1, x3), t)
		t = fma32(noFMA32Mul(f, g12), noFMA32Add(x0, x4), t)
		*y(i) = t
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
	if g1 == 0 {
		return
	}
	// Constant-filter tail (libopus comb_filter_const): rolling taps x1..x4
	// carry over from the overlap loop. SHL32(.,1) is a no-op in the float build.
	if combUsesSSE {
		// The x86 C kernel handles the original constant-body four-sample
		// prefix with grouped side products, independently of the QEXT phase
		// storage. Its scalar remainder follows below.
		for end := i + ((n - i) &^ 3); i < end; i++ {
			x0 := x(i - t1 + 2)
			*y(i) = combFilterConstSSEValue(x(i), g10, g11, g12, x2, x1, x3, x0, x4)
			x4, x3, x2, x1 = x3, x2, x1, x0
		}
	}
	for ; i < n; i++ {
		x0 := x(i - t1 + 2)
		t := x(i)
		t = fma32(g10, x2, t)
		t = fma32(g11, noFMA32Add(x1, x3), t)
		t = fma32(g12, noFMA32Add(x0, x4), t)
		*y(i) = t
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}

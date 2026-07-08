//go:build gopus_qext

package celt

// Native 96 kHz (HD / QEXT) comb-filter prefilter core.
//
// At 96 kHz libopus comb_filter dispatches to comb_filter_qext when
// mode->overlap==240 (celt/celt.c). The prefilter (run_prefilter) calls
// comb_filter with x != y (the de-emphasised input pre[c] as the delay line,
// the in[] frame as the destination), so this is the x!=y branch of
// comb_filter_qext: each even/odd sample phase is filtered independently with a
// half-rate window (new_window[i] = window[2*i+s]) at N/2 and overlap/2, reading
// 2*COMBFILTER_MAXPERIOD samples of input history (x[2*i+s-2*COMBFILTER_MAXPERIOD]).
// This doubles the comb period and tap spacing, mirroring the filter around
// 24 kHz.

// combFilterWithInputSigQEXT applies libopus comb_filter_qext for the prefilter
// (x != y). It filters dst[start:start+n] from the src delay line, splitting
// src into even/odd phases that reach back 2*COMBFILTER_MAXPERIOD samples before
// start, running the plain comb filter on each phase at n/2 with overlap/2 and a
// half-rate window, then re-interleaving into dst.
//
// libopus comb_filter_qext (x!=y) keeps the input delay line (mem_buf, from x)
// and the output (buf, from y) in SEPARATE buffers: comb_filter reads x from
// mem_buf and writes y into buf, so an already-written output sample is never
// read back as input. We mirror that with distinct phaseIn / phaseOut buffers.
func combFilterWithInputSigQEXT(dst, src []celtSig, start, t0, t1, n int, g0, g1 float32, tapset0, tapset1 int, window []float32, overlap int) {
	if n <= 0 {
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
	n2 := n / 2
	overlap2 := overlap / 2
	if n2 <= 0 {
		return
	}

	newWindow := make([]float32, overlap2)
	phaseIn := make([]float32, combFilterMaxPeriod+n2)
	phaseOut := make([]float32, n2)

	for s := 0; s < 2; s++ {
		for i := 0; i < overlap2; i++ {
			newWindow[i] = window[2*i+s]
		}
		// mem_buf[i] = x[2*i+s - 2*COMBFILTER_MAXPERIOD], x indexed from start.
		for i := 0; i < combFilterMaxPeriod+n2; i++ {
			phaseIn[i] = float32(src[start+2*i+s-hd96kCombHistory])
		}
		combFilterScalarFloat32Out(phaseOut, phaseIn, combFilterMaxPeriod, t0, t1, n2, g0, g1, tapset0, tapset1, newWindow, overlap2)
		for i := 0; i < n2; i++ {
			dst[start+2*i+s] = celtSig(phaseOut[i])
		}
	}
}

// combFilterScalarFloat32Out is the x!=y form of libopus comb_filter (float
// core): it reads the input delay line from in (where in[history+k] is x[k],
// k<0 reaching into history) and writes the n filtered samples to out. Input
// and output never alias, matching comb_filter_qext's mem_buf/buf split.
func combFilterScalarFloat32Out(out, in []float32, history, t0, t1, n int, g0, g1 float32, tapset0, tapset1 int, window []float32, overlap int) {
	if n <= 0 {
		return
	}
	if g0 == 0 && g1 == 0 {
		for i := 0; i < n; i++ {
			out[i] = in[history+i]
		}
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

	// x is in shifted so x[k] == in[history+k]; negative k reads history.
	x := func(k int) float32 { return in[history+k] }

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
		f := window[i] * window[i]
		oneMinus := float32(1.0) - f
		out[i] = x(i) +
			noFMA32Mul(oneMinus*g00, x(i-t0)) +
			noFMA32Mul(oneMinus*g01, x(i-t0+1)+x(i-t0-1)) +
			noFMA32Mul(oneMinus*g02, x(i-t0+2)+x(i-t0-2)) +
			noFMA32Mul(f*g10, x2) +
			noFMA32Mul(f*g11, x1+x3) +
			noFMA32Mul(f*g12, x0+x4)
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
	if g1 == 0 {
		for ; i < n; i++ {
			out[i] = x(i)
		}
		return
	}
	// Constant-filter tail (libopus comb_filter_const): rolling taps x1..x4
	// carry over from the overlap loop. SHL32(.,1) is a no-op in the float build.
	for ; i < n; i++ {
		x0 := x(i - t1 + 2)
		t := x(i)
		t += noFMA32Mul(g10, x2)
		t += noFMA32Mul(g11, x1+x3)
		t += noFMA32Mul(g12, x0+x4)
		out[i] = t
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}

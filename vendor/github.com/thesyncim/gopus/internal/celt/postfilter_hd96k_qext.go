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
// of synthesized history, so the HD path keeps its own per-channel history of
// the last 2*COMBFILTER_MAXPERIOD post-postfilter samples.
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

// applyHD96kPostfilterInterleaved runs the native 96 kHz postfilter on
// interleaved PCM (mono or stereo).
func (d *Decoder) applyHD96kPostfilterInterleaved(samples []float32, frameSize, lm int, newPeriod int, newGain float32, newTapset int) {
	channels := int(d.channels)
	if channels == 1 {
		d.applyHD96kPostfilterMono(samples, frameSize, lm, newPeriod, newGain, newTapset)
		return
	}
	if len(samples) < frameSize*channels {
		return
	}
	work := ensureFloat32Slice(&d.postfilterScratchF32, frameSize*2)
	left := work[:frameSize]
	right := work[frameSize : frameSize*2]
	for i := 0; i < frameSize; i++ {
		left[i] = samples[i*channels]
		right[i] = samples[i*channels+1]
	}
	d.applyHD96kPostfilterStereoPlanar(left, right, frameSize, lm, newPeriod, newGain, newTapset)
	for i := 0; i < frameSize; i++ {
		samples[i*channels] = left[i]
		samples[i*channels+1] = right[i]
	}
}

// applyHD96kPostfilterMono runs the native 96 kHz comb-filter postfilter in
// place on one channel's frame and advances the per-channel history. It mirrors
// libopus celt_decode_with_ec()'s two comb_filter calls (segment 0 of length
// shortMdctSize with the old->new parameter cross-fade, segment 1 of length
// N-shortMdctSize with constant new parameters) dispatched through
// comb_filter_qext.
func (d *Decoder) applyHD96kPostfilterMono(samples []float32, frameSize, lm int, newPeriod int, newGain float32, newTapset int) {
	channels := int(d.channels)
	if channels < 1 {
		channels = 1
	}
	qs := d.ensureQEXTState()
	if len(qs.hd96kPostMem) < hd96kCombHistory*channels {
		qs.hd96kPostMem = make([]float32, hd96kCombHistory*channels)
	}
	hist := qs.hd96kPostMem[:hd96kCombHistory]
	d.hd96kPostfilterChannel(samples[:frameSize], hist, frameSize, lm, newPeriod, newGain, newTapset)
	d.commitHD96kPostfilterState(lm, newPeriod, newGain, newTapset)
}

// applyHD96kPostfilterStereoPlanar runs the native 96 kHz postfilter on planar
// left/right channels.
func (d *Decoder) applyHD96kPostfilterStereoPlanar(left, right []float32, frameSize, lm int, newPeriod int, newGain float32, newTapset int) {
	qs := d.ensureQEXTState()
	if len(qs.hd96kPostMem) < hd96kCombHistory*2 {
		qs.hd96kPostMem = make([]float32, hd96kCombHistory*2)
	}
	histL := qs.hd96kPostMem[:hd96kCombHistory]
	histR := qs.hd96kPostMem[hd96kCombHistory : 2*hd96kCombHistory]
	d.hd96kPostfilterChannel(left[:frameSize], histL, frameSize, lm, newPeriod, newGain, newTapset)
	d.hd96kPostfilterChannel(right[:frameSize], histR, frameSize, lm, newPeriod, newGain, newTapset)
	d.commitHD96kPostfilterState(lm, newPeriod, newGain, newTapset)
}

func (d *Decoder) commitHD96kPostfilterState(lm int, newPeriod int, newGain float32, newTapset int) {
	d.postfilterPeriodOld = d.postfilterPeriod
	d.postfilterGainOld = d.postfilterGain
	d.postfilterTapsetOld = d.postfilterTapset
	d.postfilterPeriod = int32(newPeriod)
	d.postfilterGain = newGain
	d.postfilterTapset = int32(newTapset)
	if lm != 0 {
		d.postfilterPeriodOld = d.postfilterPeriod
		d.postfilterGainOld = d.postfilterGain
		d.postfilterTapsetOld = d.postfilterTapset
	}
}

// hd96kPostfilterChannel filters one channel's frame in place using hist as the
// 2*COMBFILTER_MAXPERIOD-sample post-postfilter history, then refreshes hist
// with the filtered tail. Periods/gains/tapsets are read from the decoder's
// postfilter cross-fade state (old -> current) and the incoming new params.
func (d *Decoder) hd96kPostfilterChannel(samples []float32, hist []float32, frameSize, lm int, newPeriod int, newGain float32, newTapset int) {
	d.clampDecodePostfilterPeriods()
	t0 := int(d.postfilterPeriodOld)
	t1 := int(d.postfilterPeriod)
	g0 := d.postfilterGainOld
	g1 := d.postfilterGain
	tap0 := int(d.postfilterTapsetOld)
	tap1 := int(d.postfilterTapset)
	t2 := newPeriod
	g2 := newGain
	tap2 := newTapset
	t0, t1, tap0, tap1 = sanitizePostfilterParams(t0, t1, g0, g1, tap0, tap1)
	t1b, t2, tap1b, tap2 := sanitizePostfilterParams(t1, t2, g1, g2, tap1, tap2)

	overlap := d.synthOverlapLen()
	window := GetWindowBufferF32(overlap)

	shortMdctSize := frameSize >> uint(lm)
	if shortMdctSize <= 0 || shortMdctSize > frameSize {
		shortMdctSize = frameSize
	}

	// Build the contiguous timeline [history | frame] so the comb filter can
	// read negative delays out of the synthesized history.
	qs := d.ensureQEXTState()
	tl := ensureFloat32Slice(&qs.hd96kPostTimeline, hd96kCombHistory+frameSize)
	copy(tl[:hd96kCombHistory], hist)
	copy(tl[hd96kCombHistory:], samples[:frameSize])

	base := hd96kCombHistory
	combFilterQEXTFloat32(tl, base, t0, t1, shortMdctSize, g0, g1, tap0, tap1, window, overlap, &qs.hd96kPostPhase)
	if lm != 0 && shortMdctSize < frameSize {
		combFilterQEXTFloat32(tl, base+shortMdctSize, t1b, t2, frameSize-shortMdctSize, g1, g2, tap1b, tap2, window, overlap, &qs.hd96kPostPhase)
	}

	copy(samples[:frameSize], tl[base:base+frameSize])
	// Refresh history with the last 2*MAXPERIOD samples of the timeline.
	copy(hist, tl[frameSize:frameSize+hd96kCombHistory])
}

// combFilterQEXTFloat32 applies libopus comb_filter_qext in place over tl at
// [pos, pos+n): it deinterleaves the timeline (which extends 2*MAXPERIOD samples
// before pos) into even/odd phases, runs the plain comb filter on each phase at
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
		f := window[i] * window[i]
		oneMinus := float32(1.0) - f
		*y(i) = x(i) +
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
		*y(i) = t
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}

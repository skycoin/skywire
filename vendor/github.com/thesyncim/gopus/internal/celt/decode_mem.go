package celt

// The CELT decoder keeps one synthesis delay line per channel, libopus
// CELTDecoder._decode_mem (celt/celt_decoder.c): decode_mem[c] holds
// DECODE_BUFFER_SIZE samples of decoded history followed by overlap samples of
// MDCT overlap. Every frame of N samples moves the line N samples to the left,
// the inverse MDCT writes out_syn = decode_mem[c]+DECODE_BUFFER_SIZE-N, the
// comb-filter postfilter runs in place on out_syn reading its history from the
// samples before it, and de-emphasis reads out_syn. Packet-loss concealment,
// prefilter_and_fold and the neural PLC update read and write the same line.
//
// Each channel reserves twice its line, where the line is decode_mem[c]
// preceded by decodeMemCombHeadroom() samples of older history. The line is
// the window of decodeMemLineLen() samples at decodeMemOff within the
// reservation, so the per-frame OPUS_MOVE(decode_mem[c], decode_mem[c]+N, ...)
// advances decodeMemOff by N and copies the retained samples back to the start
// of the reservation only when the window reaches its end.

// decodeMemHistoryLen is libopus QEXT_SCALE(DECODE_BUFFER_SIZE), the decoded
// history length of decode_mem.
func (d *Decoder) decodeMemHistoryLen() int {
	return d.plcDecodeBufferLen()
}

// decodeMemLen is the length of one channel's decode_mem: the history plus the
// MDCT overlap.
func (d *Decoder) decodeMemLen() int {
	return d.decodeMemHistoryLen() + d.synthOverlapLen()
}

// decodeMemCombHeadroom is the number of samples each channel retains before
// decode_mem[c]. The comb filter reads up to combFilterHistory samples before
// out_syn = decode_mem[c]+DECODE_BUFFER_SIZE-N. Every standard geometry keeps
// that history inside decode_mem, so the headroom is zero and the line is the
// libopus decode_mem. A 96 kHz QEXT custom mode with 2048-sample frames has
// qext_scale 1, so out_syn starts at decode_mem[c] itself and libopus reads
// before its buffer (reports/validation.md#reference-boundary). There the
// headroom keeps the samples that left decode_mem, and the comb filter reads
// the continuous synthesis history.
func (d *Decoder) decodeMemCombHeadroom() int {
	return max(0, combFilterHistory+d.customFrameSize-d.decodeMemHistoryLen())
}

// decodeMemLineLen is the length of one channel's line: the comb-filter
// headroom followed by decode_mem[c].
func (d *Decoder) decodeMemLineLen() int {
	return d.decodeMemCombHeadroom() + d.decodeMemLen()
}

// ensureDecodeMem sizes decode_mem for the active mode and channel count. A
// size change clears it, as libopus allocates a decoder state per mode.
func (d *Decoder) ensureDecodeMem() {
	channels := max(int(d.channels), 1)
	if n := 2 * d.decodeMemLineLen() * channels; len(d.decodeMem) != n {
		d.decodeMem = make([]celtSig, n)
		d.decodeMemOff = 0
	}
}

// clearDecodeMem zeroes decode_mem (libopus OPUS_RESET_STATE).
func (d *Decoder) clearDecodeMem() {
	d.ensureDecodeMem()
	clear(d.decodeMem)
	d.decodeMemOff = 0
}

// decodeMemChannel returns decode_mem[c].
func (d *Decoder) decodeMemChannel(c int) []celtSig {
	line := d.decodeMemLineLen()
	base := c*2*line + d.decodeMemOff
	return d.decodeMem[base+d.decodeMemCombHeadroom() : base+line : base+line]
}

// decodeMemLine returns channel c's line, decode_mem[c] preceded by the
// comb-filter headroom.
func (d *Decoder) decodeMemLine(c int) []celtSig {
	line := d.decodeMemLineLen()
	base := c*2*line + d.decodeMemOff
	return d.decodeMem[base : base+line : base+line]
}

// shiftDecodeMem moves every channel's line n samples to the left, libopus
// OPUS_MOVE(decode_mem[c], decode_mem[c]+N, DECODE_BUFFER_SIZE-N+overlap)
// extended over the comb-filter headroom. The last n samples of the line are
// left for the caller to write.
func (d *Decoder) shiftDecodeMem(n int) {
	d.ensureDecodeMem()
	size := d.decodeMemLineLen()
	off := d.decodeMemOff + n
	if off+size > 2*size {
		keep := size - n
		for c := range max(int(d.channels), 1) {
			res := d.decodeMem[c*2*size : (c+1)*2*size]
			copy(res[:keep], res[off:off+keep])
		}
		off = 0
	}
	d.decodeMemOff = off
}

// commitInterleavedToDecodeMem advances decode_mem by frameSize and stores the
// interleaved concealment output samples (frameSize+overlap per channel) as
// the newest decoded samples and the next frame's MDCT overlap, the
// decode_mem[c]+DECODE_BUFFER_SIZE-N region celt_decode_lost() writes.
func (d *Decoder) commitInterleavedToDecodeMem(samples []float32, frameSize int) {
	d.shiftDecodeMem(frameSize)
	d.commitInterleavedSamples(samples, frameSize)
}

// commitInterleavedSamples stores interleaved samples (frameSize+overlap per
// channel) at decode_mem[c]+DECODE_BUFFER_SIZE-frameSize of an already moved
// decode_mem.
func (d *Decoder) commitInterleavedSamples(samples []float32, frameSize int) {
	channels := int(d.channels)
	start := d.decodeMemHistoryLen() - frameSize
	for c := range channels {
		dst := d.decodeMemChannel(c)[start:]
		src := samples[c:]
		for i := range dst {
			dst[i] = celtSig(src[i*channels])
		}
	}
}

// outSyn returns libopus out_syn[c] = decode_mem[c]+DECODE_BUFFER_SIZE-N
// together with the overlap samples after it.
func (d *Decoder) outSyn(c, n int) []celtSig {
	return d.decodeMemChannel(c)[d.decodeMemHistoryLen()-n:]
}

// synthesizeFrame is the synthesis tail of libopus celt_decode_with_ec() for
// one frame of n samples per channel: it moves decode_mem by n, runs the
// inverse MDCT of each channel's denormalised spectrum straight into out_syn
// (celt_synthesis), applies the comb-filter postfilter in place on out_syn and
// de-emphasises out_syn. specR is the right-channel spectrum of a stereo
// decoder; a mono stream on a stereo decoder passes the mono spectrum for both
// channels. The output goes to directOutPCM when it is set; otherwise
// synthesizeFrame returns the interleaved PCM in decoder scratch.
func (d *Decoder) synthesizeFrame(specL, specR []float32, n, lm, shortBlocks int, transient bool, newPeriod int, newGain float32, newTapset int) []float32 {
	d.synthesizeToDecodeMem(specL, specR, n, shortBlocks, transient)
	d.postfilterDecodeMem(n, lm, newPeriod, newGain, newTapset)
	return d.deemphasisDecodeMem(n)
}

// synthesizeToDecodeMem moves decode_mem by n and runs celt_synthesis's
// inverse MDCTs into out_syn.
func (d *Decoder) synthesizeToDecodeMem(specL, specR []float32, n, shortBlocks int, transient bool) {
	d.shiftDecodeMem(n)
	overlap := d.synthOverlapLen()
	shortCoeffs := ensureFloat32Slice(&d.scratchShortCoeffsF32, n)
	trace := d.synthTrace
	for c := range int(d.channels) {
		spec, scratch := specL, &d.scratchIMDCTF32
		if c == 1 {
			spec, scratch = specR, &d.scratchIMDCTF32R
		}
		if trace != nil {
			trace.captureSpec(c, spec[:n])
		}
		out := d.outSyn(c, n)[: n+overlap : n+overlap]
		synthesizeChannelWithOverlapScratchF32(spec[:n], out[:overlap], overlap, transient, shortBlocks, out, scratch, shortCoeffs)
		if trace != nil {
			trace.captureIMDCT(c, out[:n])
		}
	}
}

// deemphasisDecodeMem de-emphasises out_syn into directOutPCM, or into decoder
// scratch that it returns when no output buffer is set.
func (d *Decoder) deemphasisDecodeMem(n int) []float32 {
	start := d.decodeMemHistoryLen() - n
	x0 := d.decodeMemChannel(0)[start : start+n]
	var x1 []float32
	if d.channels == 2 {
		x1 = d.decodeMemChannel(1)[start : start+n]
	}
	if d.synthTrace != nil {
		d.synthTrace.capturePostComb(0, x0)
		if x1 != nil {
			d.synthTrace.capturePostComb(1, x1)
		}
	}
	if d.directOutPCM != nil {
		d.deemphasis(d.directOutPCM, x0, x1, 1, n, d.outputDownsample(d.directOutPCM, n), d.directOutAccum)
		return nil
	}
	pcm := ensureFloat32Slice(&d.scratchPCM, n*int(d.channels))
	d.deemphasis(pcm, x0, x1, 1, n, 1, false)
	return pcm
}

// commitPostfilterState advances the postfilter parameters after a frame, as
// celt_decode_with_ec() does after its comb_filter calls.
func (d *Decoder) commitPostfilterState(lm int, newPeriod int, newGain float32, newTapset int) {
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

// postfilterDecodeMem runs celt_decode_with_ec()'s two comb_filter calls in
// place on every channel's out_syn of n samples: the first shortMdctSize
// samples cross-fade from the previous to the current parameters, the rest
// from the current to the new ones. The comb filter reads its history from
// the line before out_syn.
func (d *Decoder) postfilterDecodeMem(n, lm int, newPeriod int, newGain float32, newTapset int) {
	if d.hd96kPostfilterActive() {
		d.hd96kPostfilterDecodeMem(n, lm, newPeriod, newGain, newTapset)
		return
	}
	d.clampDecodePostfilterPeriods()
	if d.postfilterGainOld != 0 || d.postfilterGain != 0 || newGain != 0 {
		trace := d.synthTrace
		var rawPeriodOld, rawPeriod, rawTapsetOld, rawTapset int32
		var rawGainOld, rawGain float32
		if trace != nil {
			rawPeriodOld, rawPeriod = d.postfilterPeriodOld, d.postfilterPeriod
			rawGainOld, rawGain = d.postfilterGainOld, d.postfilterGain
			rawTapsetOld, rawTapset = d.postfilterTapsetOld, d.postfilterTapset
		}
		g0, g1 := d.postfilterGainOld, d.postfilterGain
		t0, t1, tap0, tap1 := sanitizePostfilterParams(int(d.postfilterPeriodOld), int(d.postfilterPeriod), g0, g1, int(d.postfilterTapsetOld), int(d.postfilterTapset))
		t1b, t2, tap1b, tap2 := sanitizePostfilterParams(t1, newPeriod, g1, newGain, tap1, newTapset)
		overlap := d.synthOverlapLen()
		windowSq := d.postfilterWindowSquareF32(overlap)
		shortMdctSize := n >> uint(lm)
		if shortMdctSize <= 0 || shortMdctSize > n {
			shortMdctSize = n
		}
		start := d.decodeMemCombHeadroom() + d.decodeMemHistoryLen() - n
		for c := range int(d.channels) {
			x := d.decodeMemLine(c)
			if trace != nil && c == 0 && d.channels == 1 {
				trace.captureMonoCombFilterInputs(
					n, lm, overlap, combFilterHistory, postfilterHistoryNeed(t0, t1, t1b, t2),
					rawPeriodOld, rawPeriod, rawGainOld, rawGain, rawTapsetOld, rawTapset,
					newPeriod, newGain, newTapset,
					t0, t1, t1b, t2, tap0, tap1, tap1b, tap2,
					g0, g1, newGain, x[start:start+n], x[start-combFilterHistory:start],
					d.scratchIMDCTF32.modeWindow(overlap), windowSq,
				)
			}
			combFilterInPlace(x, start, t0, t1, shortMdctSize, g0, g1, tap0, tap1, windowSq, overlap)
			if lm != 0 && shortMdctSize < n {
				combFilterInPlace(x, start+shortMdctSize, t1b, t2, n-shortMdctSize, g1, newGain, tap1b, tap2, windowSq, overlap)
			}
			if trace != nil && c == 0 && d.channels == 1 {
				trace.captureMonoCombFilterOutput(x[start : start+n])
			}
		}
	}
	d.commitPostfilterState(lm, newPeriod, newGain, newTapset)
}

// combFilterInPlace is libopus comb_filter(y, x, ...) with y == x == x[start:]
// for n samples. Like the C, it filters in place, so the delayed taps of each
// output read the already filtered samples before it in x. windowSq holds the
// squared mode window for the overlap cross-fade from (t0, g0, tapset0) to
// (t1, g1, tapset1).
func combFilterInPlace(x []float32, start, t0, t1, n int, g0, g1 float32, tapset0, tapset1 int, windowSq []float32, overlap int) {
	if n <= 0 || g0 == 0 && g1 == 0 {
		return
	}
	t0 = max(t0, combFilterMinPeriod)
	t1 = max(t1, combFilterMinPeriod)
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

	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	}
	overlap = min(overlap, n, len(windowSq))
	y := x[start : start+n]
	i := 0
	if overlap > 0 {
		d0 := x[start-t0-2 : start-t0-2+overlap+4]
		d1 := x[start-t1-2 : start-t1-2+overlap+4]
		combFilterOverlap(y[:overlap], d0, d1, windowSq[:overlap], g00, g01, g02, g10, g11, g12)
		i = overlap
	}
	if g1 == 0 || i >= n {
		return
	}
	// x4..x1 carry x[i-T-2..i-T+1]; delay[k] is x[i+k-T+2].
	base := start + i - t1 - 2
	x4, x3, x2, x1 := x[base], x[base+1], x[base+2], x[base+3]
	combFilterConstFloat32(y[i:], x[base+4:start+n-t1+2], g10, g11, g12, x4, x3, x2, x1, (n-i)&^3)
}

package celt

// synthesisStageTrace captures per-channel intermediate CELT synthesis buffers
// for one decoded frame so host-only float parity drift can be localised to a
// single stage. It is populated only when Decoder.synthTrace is non-nil, which
// production code never sets; the capture points are guarded by a nil check so
// the hot synthesis path keeps its zero-allocation behavior.
type synthesisStageTrace struct {
	// captured reports whether a frame populated the trace.
	captured bool
	// channels is the number of synthesized channels (1 or 2).
	channels int
	// n is the per-channel frame size in samples.
	n int
	// spec holds the post-denormalise frequency-domain buffer per channel.
	spec [2][]float32
	// imdct holds the post-IMDCT / overlap-add time buffer per channel
	// (before the comb-filter postfilter runs).
	imdct [2][]float32
	// postComb holds the time buffer after the comb-filter postfilter and before
	// de-emphasis, when the direct float output path captures it.
	postComb [2][]float32
	// qextEnergy records the decoded QEXT log energies for diagnostic parity
	// tests. It is populated only when a trace is armed.
	qextEnergy           [2][]float32
	baseEnergy           [2][]float32
	baseNorm             [2][]float32
	qextNorm             [2][]float32
	antiCollapseNormPre  [2][]float32
	antiCollapseNormPost [2][]float32
	collapseMasks        []byte
	antiCollapseSeed     uint32
	antiCollapsePulses   []int32
	combFilter           synthesisCombFilterTrace
}

// synthesisCombFilterTrace records a mono LM=0 postfilter call when synthesis
// tracing is enabled. The longstream oracle uses one such call per frame.
type synthesisCombFilterTrace struct {
	captured  bool
	invalid   bool
	callCount int

	frameSize   int
	lm          int
	overlap     int
	history     int
	historyNeed int

	rawPeriodOld int32
	rawPeriod    int32
	rawGainOld   float32
	rawGain      float32
	rawTapsetOld int32
	rawTapset    int32
	newPeriod    int
	newGain      float32
	newTapset    int

	t0, t1, t1b, t2         int
	tap0, tap1, tap1b, tap2 int
	g0, g1, g2              float32
	// historySamples is the decode_mem span before out_syn that the comb
	// filter reads.
	historySamples                  []float32
	input, window, windowSq, output []float32
	tapCoefficients                 [6]float32
}

// EnableSynthesisStageTrace arms intermediate-stage capture for the next decoded
// frame. Test-only. The returned value is also stored on the decoder; call
// SynthesisStageTrace after decoding to read the captured buffers.
func (d *Decoder) EnableSynthesisStageTrace() *synthesisStageTrace {
	t := &synthesisStageTrace{}
	d.synthTrace = t
	return t
}

// SynthesisStageTrace returns the active synthesis-stage trace, if any.
func (d *Decoder) SynthesisStageTrace() *synthesisStageTrace {
	return d.synthTrace
}

// Captured reports whether the trace was populated by a decode.
func (t *synthesisStageTrace) Captured() bool { return t != nil && t.captured }

// Channels returns the number of synthesized channels captured.
func (t *synthesisStageTrace) Channels() int { return t.channels }

// N returns the per-channel frame size captured.
func (t *synthesisStageTrace) N() int { return t.n }

// Spec returns the post-denormalise frequency buffer for the given channel.
func (t *synthesisStageTrace) Spec(ch int) []float32 {
	if ch < 0 || ch >= len(t.spec) {
		return nil
	}
	return t.spec[ch]
}

// IMDCT returns the post-IMDCT time buffer for the given channel.
func (t *synthesisStageTrace) IMDCT(ch int) []float32 {
	if ch < 0 || ch >= len(t.imdct) {
		return nil
	}
	return t.imdct[ch]
}

// PostComb returns the postfilter output before de-emphasis.
func (t *synthesisStageTrace) PostComb(ch int) []float32 {
	if ch < 0 || ch >= len(t.postComb) {
		return nil
	}
	return t.postComb[ch]
}

func (t *synthesisStageTrace) QEXTEnergy(ch int) []float32 {
	if ch < 0 || ch >= len(t.qextEnergy) {
		return nil
	}
	return t.qextEnergy[ch]
}

func (t *synthesisStageTrace) BaseEnergy(ch int) []float32 {
	if ch < 0 || ch >= len(t.baseEnergy) {
		return nil
	}
	return t.baseEnergy[ch]
}

func (t *synthesisStageTrace) BaseNorm(ch int) []float32 {
	if ch < 0 || ch >= len(t.baseNorm) {
		return nil
	}
	return t.baseNorm[ch]
}

func (t *synthesisStageTrace) QEXTNorm(ch int) []float32 {
	if ch < 0 || ch >= len(t.qextNorm) {
		return nil
	}
	return t.qextNorm[ch]
}

func (t *synthesisStageTrace) captureBaseNorm(ch int, coeffs []celtNorm, n int) {
	if t == nil || ch < 0 || ch >= len(t.baseNorm) || n <= 0 || len(coeffs) < n {
		return
	}
	out := make([]float32, n)
	for i := range n {
		out[i] = float32(coeffs[i])
	}
	t.baseNorm[ch] = out
}

func (t *synthesisStageTrace) captureAntiCollapsePre(coeffsL, coeffsR []celtNorm, channels, n int, collapse []byte, pulses []int32, seed uint32) {
	if t == nil {
		return
	}
	t.captureAntiCollapseNorm(&t.antiCollapseNormPre, coeffsL, coeffsR, channels, n)
	t.collapseMasks = append(t.collapseMasks[:0], collapse...)
	t.antiCollapseSeed = seed
	t.antiCollapsePulses = append(t.antiCollapsePulses[:0], pulses...)
}

func (t *synthesisStageTrace) captureAntiCollapsePost(coeffsL, coeffsR []celtNorm, channels, n int) {
	if t == nil {
		return
	}
	t.captureAntiCollapseNorm(&t.antiCollapseNormPost, coeffsL, coeffsR, channels, n)
}

func (t *synthesisStageTrace) captureAntiCollapseNorm(dst *[2][]float32, coeffsL, coeffsR []celtNorm, channels, n int) {
	if channels <= 0 || channels > len(dst) || n <= 0 {
		return
	}
	for ch := range channels {
		coeffs := coeffsL
		if ch == 1 {
			coeffs = coeffsR
		}
		count := n
		if count > len(coeffs) {
			count = len(coeffs)
		}
		if count <= 0 {
			return
		}
		out := make([]float32, count)
		for i := range count {
			out[i] = float32(coeffs[i])
		}
		dst[ch] = out
	}
}

func (t *synthesisStageTrace) captureQEXTNorm(ch int, coeffs []celtNorm, n int) {
	if t == nil || ch < 0 || ch >= len(t.qextNorm) || n <= 0 || len(coeffs) < n {
		return
	}
	out := make([]float32, n)
	for i := range n {
		out[i] = float32(coeffs[i])
	}
	t.qextNorm[ch] = out
}

func (t *synthesisStageTrace) captureBaseEnergy(energies []celtGLog, bands, channels int) {
	if t == nil || bands <= 0 || channels <= 0 || channels > len(t.baseEnergy) || len(energies) < bands*channels {
		return
	}
	for ch := range channels {
		out := make([]float32, bands)
		for band := range bands {
			out[band] = float32(energies[ch*bands+band])
		}
		t.baseEnergy[ch] = out
	}
}

func (t *synthesisStageTrace) captureQEXTEnergy(energies []celtGLog, bands, channels int) {
	if t == nil || bands <= 0 || channels <= 0 || channels > len(t.qextEnergy) || len(energies) < bands*channels {
		return
	}
	for ch := range channels {
		out := make([]float32, bands)
		for band := range bands {
			out[band] = float32(energies[ch*bands+band])
		}
		t.qextEnergy[ch] = out
	}
}

// captureSpec snapshots a per-channel post-denormalise spectrum buffer.
func (t *synthesisStageTrace) captureSpec(ch int, spec []float32) {
	if t == nil || ch < 0 || ch >= len(t.spec) {
		return
	}
	buf := make([]float32, len(spec))
	copy(buf, spec)
	t.spec[ch] = buf
}

// captureIMDCT snapshots a per-channel post-IMDCT time buffer and finalizes the
// trace dimensions.
func (t *synthesisStageTrace) captureIMDCT(ch int, samples []float32) {
	if t == nil || ch < 0 || ch >= len(t.imdct) {
		return
	}
	buf := make([]float32, len(samples))
	copy(buf, samples)
	t.imdct[ch] = buf
	if ch+1 > t.channels {
		t.channels = ch + 1
	}
	if len(samples) > t.n {
		t.n = len(samples)
	}
	t.captured = true
}

func (t *synthesisStageTrace) capturePostComb(ch int, samples []float32) {
	if t == nil || ch < 0 || ch >= len(t.postComb) {
		return
	}
	buf := make([]float32, len(samples))
	copy(buf, samples)
	t.postComb[ch] = buf
}

func (t *synthesisStageTrace) captureMonoCombFilterInputs(
	frameSize, lm, overlap, history, historyNeed int,
	rawPeriodOld, rawPeriod int32, rawGainOld, rawGain float32,
	rawTapsetOld, rawTapset int32, newPeriod int, newGain float32, newTapset int,
	t0, t1, t1b, t2, tap0, tap1, tap1b, tap2 int,
	g0, g1, g2 float32, samples, hist []celtSig,
	window, windowSq []float32,
) {
	if t == nil || lm != 0 {
		return
	}
	trace := &t.combFilter
	trace.callCount++
	if trace.captured || trace.invalid {
		return
	}
	if frameSize <= 0 || overlap < 0 || history != combFilterHistory ||
		len(samples) < frameSize || len(hist) < history ||
		len(window) < overlap || len(windowSq) < overlap {
		trace.invalid = true
		return
	}
	trace.frameSize = frameSize
	trace.lm = lm
	trace.overlap = overlap
	trace.history = history
	trace.historyNeed = historyNeed
	trace.rawPeriodOld = rawPeriodOld
	trace.rawPeriod = rawPeriod
	trace.rawGainOld = rawGainOld
	trace.rawGain = rawGain
	trace.rawTapsetOld = rawTapsetOld
	trace.rawTapset = rawTapset
	trace.newPeriod = newPeriod
	trace.newGain = newGain
	trace.newTapset = newTapset
	trace.t0, trace.t1, trace.t1b, trace.t2 = t0, t1, t1b, t2
	trace.tap0, trace.tap1, trace.tap1b, trace.tap2 = tap0, tap1, tap1b, tap2
	trace.g0, trace.g1, trace.g2 = g0, g1, g2
	trace.historySamples = append([]float32(nil), hist[len(hist)-history:]...)
	trace.input = append([]float32(nil), samples[:frameSize]...)
	trace.window = append([]float32(nil), window[:overlap]...)
	trace.windowSq = append([]float32(nil), windowSq[:overlap]...)
	for tap := range 3 {
		trace.tapCoefficients[tap] = combGain32(g0, tap0, tap)
		trace.tapCoefficients[3+tap] = combGain32(g1, tap1, tap)
	}
	trace.captured = true
}

func (t *synthesisStageTrace) captureMonoCombFilterOutput(samples []float32) {
	if t == nil || !t.combFilter.captured || len(samples) < t.combFilter.frameSize {
		return
	}
	trace := &t.combFilter
	trace.output = make([]float32, trace.frameSize)
	copy(trace.output, samples[:trace.frameSize])
}

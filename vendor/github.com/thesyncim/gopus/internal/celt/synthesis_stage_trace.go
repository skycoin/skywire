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

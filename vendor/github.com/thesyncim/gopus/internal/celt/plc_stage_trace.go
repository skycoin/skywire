package celt

// plcStageTrace captures intermediate noise-PLC concealment buffers for one
// targeted noise-PLC chunk so host-only float parity drift in the overlong /
// multi-frame PLC concealment path can be localised to a single stage. It is
// populated only when Decoder.plcStageTrace is non-nil, which production code
// never sets; the capture points are guarded by a nil check so the hot PLC path
// keeps its zero-allocation behavior.
//
// foldIndex selects which prefilter-and-fold (i.e. which noise-PLC chunk that
// consumes a pending fold) to capture, counted 0-based over the decoder's
// lifetime; this mirrors the libopus_celt_plc_stage_trace.c oracle counter.
type plcStageTrace struct {
	foldIndex int // target prefilter_and_fold call index to capture
	foldCount int // running count of prefilter_and_fold calls observed

	captured bool
	channels int
	n        int
	overlap  int
	// fold[ch] holds the per-channel post-prefilter_and_fold overlap seed for the
	// target chunk (the TDAC-blended decode_mem overlap region).
	fold [2][]float32
	// combIn[ch] / combOut[ch] hold the per-channel comb_filter input (history+
	// overlap) and output (etmp) for the fold of the target chunk.
	combIn  [2][]float32
	combOut [2][]float32
	// preSpec[ch] holds the per-channel renormalised noise coeffs (pre-denorm).
	preSpec [2][]float32
	// spec[ch] holds the per-channel denormalised noise spectrum (pre-IMDCT).
	spec [2][]float32
	// presyn[ch] holds the per-channel post-synthesis time buffer (de-interleaved)
	// before the comb post-filter runs for the target chunk.
	presyn [2][]float32
	// final holds the interleaved post-deemphasis PCM for the target chunk.
	final []float32
}

// EnablePLCStageTrace arms noise-PLC concealment-stage capture for the
// foldIndex-th prefilter-and-fold consumed during subsequent decoding.
// Test-only.
func (d *Decoder) EnablePLCStageTrace(foldIndex int) *plcStageTrace {
	t := &plcStageTrace{foldIndex: foldIndex, foldCount: -1}
	d.plcStageTrace = t
	return t
}

// PLCStageTrace returns the active PLC stage trace, if any.
func (d *Decoder) PLCStageTrace() *plcStageTrace { return d.plcStageTrace }

func (t *plcStageTrace) Captured() bool { return t != nil && t.captured }
func (t *plcStageTrace) Channels() int  { return t.channels }
func (t *plcStageTrace) N() int         { return t.n }

func (t *plcStageTrace) Overlap() int { return t.overlap }

func (t *plcStageTrace) Fold(ch int) []float32 {
	if t == nil || ch < 0 || ch >= len(t.fold) {
		return nil
	}
	return t.fold[ch]
}

func (t *plcStageTrace) PreSpec(ch int) []float32 {
	if t == nil || ch < 0 || ch >= len(t.preSpec) {
		return nil
	}
	return t.preSpec[ch]
}

func (t *plcStageTrace) capturePreSpec(ch int, spec []float32) {
	if t == nil || ch < 0 || ch >= len(t.preSpec) {
		return
	}
	buf := make([]float32, len(spec))
	copy(buf, spec)
	t.preSpec[ch] = buf
}

func (t *plcStageTrace) Spec(ch int) []float32 {
	if t == nil || ch < 0 || ch >= len(t.spec) {
		return nil
	}
	return t.spec[ch]
}

func (t *plcStageTrace) captureSpec(ch int, spec []float32) {
	if t == nil || ch < 0 || ch >= len(t.spec) {
		return
	}
	buf := make([]float32, len(spec))
	copy(buf, spec)
	t.spec[ch] = buf
}

func (t *plcStageTrace) PreSyn(ch int) []float32 {
	if t == nil || ch < 0 || ch >= len(t.presyn) {
		return nil
	}
	return t.presyn[ch]
}

func (t *plcStageTrace) CombIn(ch int) []float32 {
	if t == nil || ch < 0 || ch >= len(t.combIn) {
		return nil
	}
	return t.combIn[ch]
}

func (t *plcStageTrace) CombOut(ch int) []float32 {
	if t == nil || ch < 0 || ch >= len(t.combOut) {
		return nil
	}
	return t.combOut[ch]
}

func (t *plcStageTrace) captureCombIn(ch int, src []celtSig) {
	if t == nil || ch < 0 || ch >= len(t.combIn) {
		return
	}
	buf := make([]float32, len(src))
	for i := range src {
		buf[i] = float32(src[i])
	}
	t.combIn[ch] = buf
}

func (t *plcStageTrace) captureCombOut(ch int, etmp []celtSig) {
	if t == nil || ch < 0 || ch >= len(t.combOut) {
		return
	}
	buf := make([]float32, len(etmp))
	for i := range etmp {
		buf[i] = float32(etmp[i])
	}
	t.combOut[ch] = buf
}

// captureFold snapshots the per-channel folded overlap seed.
func (t *plcStageTrace) captureFold(overlapBuffer []celtSig, channels, segLen int) {
	if t == nil {
		return
	}
	for ch := 0; ch < channels && ch < len(t.fold); ch++ {
		buf := make([]float32, segLen)
		ovl := overlapBuffer[ch*segLen : (ch+1)*segLen]
		for i := range segLen {
			buf[i] = float32(ovl[i])
		}
		t.fold[ch] = buf
	}
	t.overlap = segLen
}

func (t *plcStageTrace) Final() []float32 {
	if t == nil {
		return nil
	}
	return t.final
}

// captureFinal snapshots the interleaved post-deemphasis PCM for the chunk.
func (t *plcStageTrace) captureFinal(pcm []float32) {
	if t == nil {
		return
	}
	buf := make([]float32, len(pcm))
	copy(buf, pcm)
	t.final = buf
}

// observeFold advances the fold counter and reports whether the target chunk is
// now being captured. Called from applyPendingPLCPrefilterAndFold.
func (t *plcStageTrace) observeFold() bool {
	if t == nil {
		return false
	}
	t.foldCount++
	return t.foldCount == t.foldIndex
}

// armed reports whether the target chunk is currently being captured.
func (t *plcStageTrace) armed() bool {
	return t != nil && t.foldCount == t.foldIndex
}

// capturePreSyn snapshots the per-channel pre-postfilter synthesis buffer from
// the interleaved noise-PLC dst.
func (t *plcStageTrace) capturePreSyn(dst []float32, frameSize, channels int) {
	if t == nil {
		return
	}
	for ch := 0; ch < channels && ch < len(t.presyn); ch++ {
		buf := make([]float32, frameSize)
		for i := range frameSize {
			buf[i] = dst[i*channels+ch]
		}
		t.presyn[ch] = buf
	}
	t.n = frameSize
	if channels > t.channels {
		t.channels = channels
	}
	t.captured = true
}

package celt

// ReserveEncodeScratch sizes the buffers reached when an Opus child encoder
// switches into CELT, including the analysis buffers that EncodeFrame grows
// after its ordinary frame scratch has been prepared.
func (e *Encoder) ReserveEncodeScratch(frameSize int) {
	if frameSize <= 0 {
		return
	}
	e.ensureScratch(frameSize)
	bands := e.predStride()
	channels := int(e.channels)
	needed := bands * channels
	if cap(e.lastBandLogE) < needed {
		grown := make([]celtGLog, len(e.lastBandLogE), needed)
		copy(grown, e.lastBandLogE)
		e.lastBandLogE = grown
	}
	if cap(e.lastBandLogE2) < needed {
		grown := make([]celtGLog, len(e.lastBandLogE2), needed)
		copy(grown, e.lastBandLogE2)
		e.lastBandLogE2 = grown
	}
	e.scratch.dynallocOldBandE = ensureGLogSlice(&e.scratch.dynallocOldBandE, needed)
	e.dynallocScratch.EnsureDynallocScratch(bands, channels)
}

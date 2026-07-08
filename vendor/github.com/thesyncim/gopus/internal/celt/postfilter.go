package celt

import "github.com/thesyncim/gopus/internal/extsupport"

const (
	combFilterMinPeriod = 15
	combFilterMaxPeriod = 1024
	combFilterHistory   = combFilterMaxPeriod + 2
	// Matches libopus DEC_PITCH_BUF_SIZE used by celt_decode_lost().
	plcDecodeBufferSize = 2048
)

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

func (d *Decoder) updatePostfilterHistory(samples []float32, frameSize int, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	d.materializePostfilterHistoryFromPLC()
	d.postfilterMemFromPLC = false
	d.postfilterMemPLCBacked = false
	if d.channels <= 1 {
		hist := d.postfilterMem[:history]
		if frameSize >= history {
			copyFloat32ToSig(hist, samples[frameSize-history:frameSize])
			return
		}
		copy(hist, hist[frameSize:])
		copyFloat32ToSig(hist[history-frameSize:], samples[:frameSize])
		return
	}
	if d.channels == 2 {
		histL := d.postfilterMem[:history]
		histR := d.postfilterMem[history : 2*history]
		if frameSize >= history {
			src := (frameSize - history) * 2
			for i := range history {
				histL[i] = celtSig(samples[src])
				histR[i] = celtSig(samples[src+1])
				src += 2
			}
			return
		}
		copy(histL, histL[frameSize:])
		copy(histR, histR[frameSize:])
		dst := history - frameSize
		src := 0
		for i := range frameSize {
			histL[dst+i] = celtSig(samples[src])
			histR[dst+i] = celtSig(samples[src+1])
			src += 2
		}
		return
	}

	channels := int(d.channels)
	for ch := range channels {
		hist := d.postfilterMem[ch*history : (ch+1)*history]
		if frameSize >= history {
			src := (frameSize-history)*channels + ch
			for i := range history {
				hist[i] = celtSig(samples[src])
				src += channels
			}
			continue
		}
		copy(hist, hist[frameSize:])
		src := ch
		dst := history - frameSize
		for i := range frameSize {
			hist[dst+i] = celtSig(samples[src])
			src += channels
		}
	}
}

func (d *Decoder) updatePLCDecodeHistory(samples []float32, frameSize int, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	d.postfilterMemFromPLC = false
	d.postfilterMemPLCBacked = false
	channels := int(d.channels)
	if len(d.plcDecodeMem) != history*channels {
		d.plcDecodeMem = make([]celtSig, history*channels)
		d.plcDecodeMemRingActive = false
		d.plcDecodeMemRingStart = 0
	}
	if d.channels == 2 && history == plcDecodeBufferSize {
		histL := d.plcDecodeMem[:history]
		histR := d.plcDecodeMem[history : 2*history]
		if frameSize >= history {
			src := (frameSize - history) * 2
			for i := range history {
				histL[i] = celtSig(samples[src])
				histR[i] = celtSig(samples[src+1])
				src += 2
			}
			d.plcDecodeMemRingActive = false
			d.plcDecodeMemRingStart = 0
			return
		}
		start := d.plcDecodeMemRingStart
		if !d.plcDecodeMemRingActive {
			start = 0
		}
		updateInterleavedStereoHistoryRingSig(histL, histR, samples, frameSize, history, start)
		start += frameSize
		if start >= history {
			start %= history
		}
		d.plcDecodeMemRingStart = start
		d.plcDecodeMemRingActive = start != 0
		return
	}
	d.materializePLCDecodeHistory()
	if d.channels <= 1 {
		hist := d.plcDecodeMem[:history]
		if frameSize >= history {
			copyFloat32ToSig(hist, samples[frameSize-history:frameSize])
			return
		}
		copy(hist, hist[frameSize:])
		copyFloat32ToSig(hist[history-frameSize:], samples[:frameSize])
		return
	}
	if d.channels == 2 {
		histL := d.plcDecodeMem[:history]
		histR := d.plcDecodeMem[history : 2*history]
		if frameSize >= history {
			src := (frameSize - history) * 2
			for i := range history {
				histL[i] = celtSig(samples[src])
				histR[i] = celtSig(samples[src+1])
				src += 2
			}
			return
		}
		copy(histL, histL[frameSize:])
		copy(histR, histR[frameSize:])
		dst := history - frameSize
		src := 0
		for i := range frameSize {
			histL[dst+i] = celtSig(samples[src])
			histR[dst+i] = celtSig(samples[src+1])
			src += 2
		}
		return
	}

	channels = int(d.channels)
	for ch := 0; ch < channels; ch++ {
		hist := d.plcDecodeMem[ch*history : (ch+1)*history]
		if frameSize >= history {
			src := (frameSize-history)*channels + ch
			for i := range history {
				hist[i] = celtSig(samples[src])
				src += channels
			}
			continue
		}
		copy(hist, hist[frameSize:])
		src := ch
		dst := history - frameSize
		for i := range frameSize {
			hist[dst+i] = celtSig(samples[src])
			src += channels
		}
	}
}

func slidePlanarHistoryPrefixSig(hist []celtSig, frameSize, history int) {
	keep := history - frameSize
	if keep <= 0 {
		return
	}
	if keep <= 128 {
		_ = hist[frameSize+keep-1]
		_ = hist[keep-1]
		copy(hist[:keep], hist[frameSize:frameSize+keep])
		return
	}
	_ = hist[frameSize+keep-1]
	_ = hist[keep-1]
	copy(hist[:keep], hist[frameSize:frameSize+keep])
}

func reverseSigInPlace(x []celtSig) {
	for i, j := 0, len(x)-1; i < j; i, j = i+1, j-1 {
		x[i], x[j] = x[j], x[i]
	}
}

func rotateSigLeftInPlace(x []celtSig, n int) {
	if len(x) == 0 {
		return
	}
	n %= len(x)
	if n == 0 {
		return
	}
	reverseSigInPlace(x[:n])
	reverseSigInPlace(x[n:])
	reverseSigInPlace(x)
}

func updateInterleavedStereoHistoryRingSig(histL, histR []celtSig, samples []float32, frameSize, history, start int) {
	if start < 0 || start >= history {
		start = 0
	}
	first := history - start
	if first > frameSize {
		first = frameSize
	}
	src := 0
	for i := 0; i < first; i++ {
		histL[start+i] = celtSig(samples[src])
		histR[start+i] = celtSig(samples[src+1])
		src += 2
	}
	for i := 0; i < frameSize-first; i++ {
		histL[i] = celtSig(samples[src])
		histR[i] = celtSig(samples[src+1])
		src += 2
	}
}

func (d *Decoder) materializePLCDecodeHistory() {
	if d == nil || !d.plcDecodeMemRingActive {
		return
	}
	history := plcDecodeBufferSize
	channels := int(d.channels)
	if channels <= 0 || len(d.plcDecodeMem) < history*channels {
		d.plcDecodeMemRingActive = false
		d.plcDecodeMemRingStart = 0
		return
	}
	start := d.plcDecodeMemRingStart
	if start <= 0 || start >= history {
		d.plcDecodeMemRingActive = false
		d.plcDecodeMemRingStart = 0
		return
	}
	for ch := range channels {
		hist := d.plcDecodeMem[ch*history : (ch+1)*history]
		rotateSigLeftInPlace(hist, start)
	}
	d.plcDecodeMemRingActive = false
	d.plcDecodeMemRingStart = 0
}

func (d *Decoder) markPostfilterHistoryFromPLC() {
	d.postfilterMemFromPLC = true
	d.postfilterMemPLCBacked = true
}

func (d *Decoder) markPostfilterHistoryMaterialized() {
	d.postfilterMemFromPLC = false
	d.postfilterMemPLCBacked = true
}

func (d *Decoder) materializePostfilterHistoryFromPLC() {
	if d == nil || !d.postfilterMemFromPLC {
		return
	}
	channels := int(d.channels)
	if channels <= 0 {
		d.postfilterMemFromPLC = false
		d.postfilterMemPLCBacked = false
		return
	}
	history := combFilterHistory
	if len(d.postfilterMem) != history*channels {
		d.postfilterMem = make([]celtSig, history*channels)
	}
	if len(d.plcDecodeMem) < plcDecodeBufferSize*channels {
		d.postfilterMemFromPLC = false
		d.postfilterMemPLCBacked = false
		return
	}
	ringStart := 0
	if d.plcDecodeMemRingActive {
		ringStart = d.plcDecodeMemRingStart
		if ringStart < 0 || ringStart >= plcDecodeBufferSize {
			ringStart = 0
		}
	}
	srcStart := ringStart + plcDecodeBufferSize - history
	if srcStart >= plcDecodeBufferSize {
		srcStart -= plcDecodeBufferSize
	}
	for ch := range channels {
		src := d.plcDecodeMem[ch*plcDecodeBufferSize : (ch+1)*plcDecodeBufferSize]
		dst := d.postfilterMem[ch*history : (ch+1)*history]
		if srcStart+history <= plcDecodeBufferSize {
			copy(dst, src[srcStart:srcStart+history])
			continue
		}
		first := plcDecodeBufferSize - srcStart
		copy(dst[:first], src[srcStart:])
		copy(dst[first:], src[:history-first])
	}
	d.markPostfilterHistoryMaterialized()
}

func (d *Decoder) materializePostfilterHistorySuffixFromPLC(need int) {
	if d == nil || !d.postfilterMemFromPLC {
		return
	}
	history := combFilterHistory
	if need >= history {
		d.materializePostfilterHistoryFromPLC()
		return
	}
	if need <= 0 {
		return
	}
	channels := int(d.channels)
	if channels <= 0 {
		d.postfilterMemFromPLC = false
		d.postfilterMemPLCBacked = false
		return
	}
	if len(d.postfilterMem) != history*channels {
		d.postfilterMem = make([]celtSig, history*channels)
	}
	if len(d.plcDecodeMem) < plcDecodeBufferSize*channels {
		d.postfilterMemFromPLC = false
		d.postfilterMemPLCBacked = false
		return
	}
	ringStart := 0
	if d.plcDecodeMemRingActive {
		ringStart = d.plcDecodeMemRingStart
		if ringStart < 0 || ringStart >= plcDecodeBufferSize {
			ringStart = 0
		}
	}
	srcStart := ringStart + plcDecodeBufferSize - need
	if srcStart >= plcDecodeBufferSize {
		srcStart -= plcDecodeBufferSize
	}
	dstStart := history - need
	for ch := range channels {
		src := d.plcDecodeMem[ch*plcDecodeBufferSize : (ch+1)*plcDecodeBufferSize]
		dst := d.postfilterMem[ch*history+dstStart : (ch+1)*history]
		if srcStart+need <= plcDecodeBufferSize {
			copy(dst, src[srcStart:srcStart+need])
			continue
		}
		first := plcDecodeBufferSize - srcStart
		copy(dst[:first], src[srcStart:])
		copy(dst[first:], src[:need-first])
	}
}

func postfilterHistoryNeed(t0, t1, t1b, t2 int) int {
	need := max(t1, t0)
	if t1b > need {
		need = t1b
	}
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

func updateMonoHistoryFromFloat32(hist []celtSig, samples []float32, frameSize, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	if frameSize >= history {
		copyFloat32ToSig(hist[:history], samples[frameSize-history:frameSize])
		return
	}
	slidePlanarHistoryPrefixSig(hist, frameSize, history)
	copyFloat32ToSig(hist[history-frameSize:history], samples[:frameSize])
}

func updatePlanarHistoryFromFloat32(hist []celtSig, samples []float32, frameSize, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	if frameSize >= history {
		copyFloat32ToSig(hist[:history], samples[frameSize-history:frameSize])
		return
	}
	slidePlanarHistoryPrefixSig(hist, frameSize, history)
	copyFloat32ToSig(hist[history-frameSize:history], samples[:frameSize])
}

func updatePlanarHistoryRingFromFloat32(hist []celtSig, samples []float32, frameSize, history, start int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	if frameSize >= history {
		copyFloat32ToSig(hist[:history], samples[frameSize-history:frameSize])
		return
	}
	if start < 0 || start >= history {
		start = 0
	}
	first := history - start
	if first > frameSize {
		first = frameSize
	}
	copyFloat32ToSig(hist[start:start+first], samples[:first])
	copyFloat32ToSig(hist[:frameSize-first], samples[first:frameSize])
}

func (d *Decoder) updatePLCDecodeHistoryStereoPlanarFromFloat32(left, right []float32, frameSize int, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	d.postfilterMemFromPLC = false
	d.postfilterMemPLCBacked = false
	channels := int(d.channels)
	if len(d.plcDecodeMem) != history*channels {
		d.plcDecodeMem = make([]celtSig, history*channels)
		d.plcDecodeMemRingActive = false
		d.plcDecodeMemRingStart = 0
	}
	histL := d.plcDecodeMem[:history]
	histR := d.plcDecodeMem[history : 2*history]
	if history != plcDecodeBufferSize || d.channels != 2 {
		d.materializePLCDecodeHistory()
		updatePlanarHistoryFromFloat32(histL, left, frameSize, history)
		updatePlanarHistoryFromFloat32(histR, right, frameSize, history)
		return
	}
	if frameSize >= history {
		copyFloat32ToSig(histL, left[frameSize-history:frameSize])
		copyFloat32ToSig(histR, right[frameSize-history:frameSize])
		d.plcDecodeMemRingActive = false
		d.plcDecodeMemRingStart = 0
		return
	}
	start := d.plcDecodeMemRingStart
	if !d.plcDecodeMemRingActive {
		start = 0
	}
	updatePlanarHistoryRingFromFloat32(histL, left, frameSize, history, start)
	updatePlanarHistoryRingFromFloat32(histR, right, frameSize, history, start)
	start += frameSize
	if start >= history {
		start %= history
	}
	d.plcDecodeMemRingStart = start
	d.plcDecodeMemRingActive = start != 0
}

func (d *Decoder) updatePLCDecodeHistoryMonoFromFloat32(samples []float32, frameSize int, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	d.postfilterMemFromPLC = false
	d.postfilterMemPLCBacked = false
	d.materializePLCDecodeHistory()
	channels := int(d.channels)
	if len(d.plcDecodeMem) != history*channels {
		d.plcDecodeMem = make([]celtSig, history*channels)
		d.plcDecodeMemRingActive = false
		d.plcDecodeMemRingStart = 0
	}
	updateMonoHistoryFromFloat32(d.plcDecodeMem[:history], samples, frameSize, history)
}

func (d *Decoder) commitPostfilterStateNoGain(lm int, newPeriod int, newGain float32, newTapset int) {
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

func (d *Decoder) applyPostfilterNoGainMonoFromFloat32(samples []float32, frameSize, lm int, newPeriod int, newGain float32, newTapset int) {
	if frameSize <= 0 {
		return
	}
	history := combFilterHistory
	channels := int(d.channels)
	if len(d.postfilterMem) != history*channels {
		d.postfilterMem = make([]celtSig, history*channels)
		d.postfilterMemFromPLC = false
		d.postfilterMemPLCBacked = false
	}
	d.clampDecodePostfilterPeriods()
	d.updatePLCDecodeHistoryMonoFromFloat32(samples, frameSize, plcDecodeBufferSize)
	d.markPostfilterHistoryFromPLC()
	d.commitPostfilterStateNoGain(lm, newPeriod, newGain, newTapset)
}

func (d *Decoder) applyPostfilterNoGainStereoPlanarFromFloat32(left, right []float32, frameSize, lm int, newPeriod int, newGain float32, newTapset int) {
	if frameSize <= 0 {
		return
	}
	history := combFilterHistory
	if len(d.postfilterMem) != history*2 {
		d.postfilterMem = make([]celtSig, history*2)
		d.postfilterMemFromPLC = false
		d.postfilterMemPLCBacked = false
	}
	d.clampDecodePostfilterPeriods()
	d.updatePLCDecodeHistoryStereoPlanarFromFloat32(left, right, frameSize, plcDecodeBufferSize)
	d.markPostfilterHistoryFromPLC()
	d.commitPostfilterStateNoGain(lm, newPeriod, newGain, newTapset)
}

func applyPostfilterChannelInPlaceFloat32(samples []float32, hist []celtSig, frameSize, history, lm int, t0, t1, t1b, t2 int, g0, g1, g2 float32, tap0, tap1, tap1b, tap2 int, window, windowSq []float32, overlap int) {
	shortMdctSize := frameSize >> uint(lm)
	if shortMdctSize <= 0 || shortMdctSize > frameSize {
		shortMdctSize = frameSize
	}

	combFilterWithSquarePlanarFloat32(samples, hist, history, 0, t0, t1, shortMdctSize, g0, g1, tap0, tap1, window, windowSq, overlap)
	if lm != 0 && shortMdctSize < frameSize {
		combFilterWithSquarePlanarFloat32(samples, hist, history, shortMdctSize, t1b, t2, frameSize-shortMdctSize, g1, g2, tap1b, tap2, window, windowSq, overlap)
	}
}

func (d *Decoder) postfilterWindowSquareF32(overlap int) []float32 {
	window := GetWindowBufferF32(overlap)
	if len(window) == 0 {
		return nil
	}
	windowSq := ensureFloat32Slice(&d.postfilterWindowSqF32, len(window))
	for i, w := range window {
		windowSq[i] = noFMA32Mul(w, w)
	}
	return windowSq
}

func (d *Decoder) applyPostfilterStereoPlanarFromFloat32(left, right []float32, frameSize, lm int, newPeriod int, newGain float32, newTapset int) {
	if len(left) < frameSize || len(right) < frameSize || frameSize <= 0 {
		return
	}

	history := combFilterHistory
	if len(d.postfilterMem) != history*2 {
		d.postfilterMem = make([]celtSig, history*2)
		d.postfilterMemFromPLC = false
		d.postfilterMemPLCBacked = false
	}
	d.clampDecodePostfilterPeriods()
	if d.postfilterGainOld == 0 && d.postfilterGain == 0 && newGain == 0 {
		d.updatePLCDecodeHistoryStereoPlanarFromFloat32(left, right, frameSize, plcDecodeBufferSize)
		d.markPostfilterHistoryFromPLC()
		d.commitPostfilterStateNoGain(lm, newPeriod, newGain, newTapset)
		return
	}

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
	d.materializePostfilterHistorySuffixFromPLC(postfilterHistoryNeed(t0, t1, t1b, t2))

	overlap := d.synthOverlapLen()
	window := GetWindowBufferF32(overlap)
	windowSq := d.postfilterWindowSquareF32(overlap)
	histL := d.postfilterMem[:history]
	histR := d.postfilterMem[history : 2*history]
	applyPostfilterChannelInPlaceFloat32(left, histL, frameSize, history, lm, t0, t1, t1b, t2, g0, g1, g2, tap0, tap1, tap1b, tap2, window, windowSq, overlap)
	applyPostfilterChannelInPlaceFloat32(right, histR, frameSize, history, lm, t0, t1, t1b, t2, g0, g1, g2, tap0, tap1, tap1b, tap2, window, windowSq, overlap)

	d.updatePLCDecodeHistoryStereoPlanarFromFloat32(left, right, frameSize, plcDecodeBufferSize)
	d.markPostfilterHistoryFromPLC()
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

func (d *Decoder) applyPostfilterFloat32(samples []float32, frameSize, lm int, newPeriod int, newGain float32, newTapset int) {
	if len(samples) == 0 || frameSize <= 0 || d.channels <= 0 {
		return
	}
	if lm < 0 {
		lm = 0
	}
	if d.hd96kPostfilterActive() {
		d.applyHD96kPostfilterInterleaved(samples, frameSize, lm, newPeriod, newGain, newTapset)
		return
	}
	if d.channels == 1 {
		if d.postfilterGainOld == 0 && d.postfilterGain == 0 && newGain == 0 {
			d.applyPostfilterNoGainMonoFromFloat32(samples[:frameSize], frameSize, lm, newPeriod, newGain, newTapset)
			return
		}
		history := combFilterHistory
		if len(d.postfilterMem) != history {
			d.postfilterMem = make([]celtSig, history)
			d.postfilterMemFromPLC = false
			d.postfilterMemPLCBacked = false
		}
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
		d.materializePostfilterHistorySuffixFromPLC(postfilterHistoryNeed(t0, t1, t1b, t2))
		overlap := d.synthOverlapLen()
		window := GetWindowBufferF32(overlap)
		windowSq := d.postfilterWindowSquareF32(overlap)
		applyPostfilterChannelInPlaceFloat32(samples[:frameSize], d.postfilterMem[:history], frameSize, history, lm, t0, t1, t1b, t2, g0, g1, g2, tap0, tap1, tap1b, tap2, window, windowSq, overlap)
		d.updatePLCDecodeHistoryMonoFromFloat32(samples[:frameSize], frameSize, plcDecodeBufferSize)
		d.markPostfilterHistoryFromPLC()
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
		return
	}

	channels := int(d.channels)
	if len(samples) < frameSize*channels {
		return
	}
	work := ensureFloat32Slice(&d.postfilterScratchF32, frameSize*2)
	left := work[:frameSize]
	right := work[frameSize : frameSize*2]
	for i := range frameSize {
		left[i] = samples[i*channels]
		right[i] = samples[i*channels+1]
	}
	d.applyPostfilterStereoPlanarFromFloat32(left, right, frameSize, lm, newPeriod, newGain, newTapset)
	for i := range frameSize {
		samples[i*channels] = left[i]
		samples[i*channels+1] = right[i]
	}
}

func combPlanarAtFloat32(samples []float32, hist []celtSig, history, pos int) float32 {
	if pos < history {
		return float32(hist[pos])
	}
	return samples[pos-history]
}

func combFilterConstValue(base, g10, g11, g12, center, plus1, minus1, plus2, minus2 float32) float32 {
	sum := base
	sum += g10 * center
	sum += g11 * (plus1 + minus1)
	sum += g12 * (plus2 + minus2)
	return sum
}

func combFilterConstFloat32Hist(dst []float32, delay []celtSig, g10, g11, g12 float32, x4, x3, x2, x1 float32) (float32, float32, float32, float32) {
	n := len(dst)
	if n == 0 {
		return x4, x3, x2, x1
	}
	delay = delay[:n:n]
	_ = dst[n-1]
	_ = delay[n-1]
	i := 0
	for ; i+4 < n; i += 5 {
		x0 := float32(delay[i])
		dst[i] = combFilterConstValue(dst[i], g10, g11, g12, x2, x1, x3, x0, x4)

		x4 = float32(delay[i+1])
		dst[i+1] = combFilterConstValue(dst[i+1], g10, g11, g12, x1, x0, x2, x4, x3)

		x3 = float32(delay[i+2])
		dst[i+2] = combFilterConstValue(dst[i+2], g10, g11, g12, x0, x4, x1, x3, x2)

		x2 = float32(delay[i+3])
		dst[i+3] = combFilterConstValue(dst[i+3], g10, g11, g12, x4, x3, x0, x2, x1)

		x1 = float32(delay[i+4])
		dst[i+4] = combFilterConstValue(dst[i+4], g10, g11, g12, x3, x2, x4, x1, x0)
	}
	for ; i < n; i++ {
		x0 := float32(delay[i])
		dst[i] = combFilterConstValue(dst[i], g10, g11, g12, x2, x1, x3, x0, x4)
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
	return x4, x3, x2, x1
}

func combFilterConstFloat32(dst, delay []float32, g10, g11, g12 float32, x4, x3, x2, x1 float32) (float32, float32, float32, float32) {
	n := len(dst)
	if n == 0 {
		return x4, x3, x2, x1
	}
	delay = delay[:n:n]
	_ = dst[n-1]
	_ = delay[n-1]
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

func combFilterWithSquarePlanarFloat32(samples []float32, hist []celtSig, history, frameOffset int, t0, t1, n int, g0, g1 float32, tapset0, tapset1 int, window, windowSq []float32, overlap int) {
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

	if overlap > n {
		overlap = n
	}
	if overlap > len(window) {
		overlap = len(window)
	}
	if windowSq != nil && overlap > len(windowSq) {
		overlap = len(windowSq)
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

	start := history + frameOffset
	base1 := start - t1 - 2
	x1 := combPlanarAtFloat32(samples, hist, history, base1+3)
	x2 := combPlanarAtFloat32(samples, hist, history, base1+2)
	x3 := combPlanarAtFloat32(samples, hist, history, base1+1)
	x4 := combPlanarAtFloat32(samples, hist, history, base1)

	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	}

	i := 0
	base0 := start - t0 - 2
	if windowSq != nil && overlap > 0 && base0 >= 0 && base1 >= 0 && base0+overlap+4 <= history && base1+overlap+4 <= history {
		delay0 := hist[base0 : base0+overlap+4]
		delay1 := hist[base1 : base1+overlap+4]
		x4 = float32(delay1[0])
		x3 = float32(delay1[1])
		x2 = float32(delay1[2])
		x1 = float32(delay1[3])
		windowSqView := windowSq[:overlap]
		for ; i < overlap; i++ {
			f := windowSqView[i]
			oneMinus := float32(1.0) - f
			x0 := float32(delay1[i+4])
			sum := samples[frameOffset+i] +
				(oneMinus*g00)*float32(delay0[i+2]) +
				(oneMinus*g01)*(float32(delay0[i+3])+float32(delay0[i+1])) +
				(oneMinus*g02)*(float32(delay0[i+4])+float32(delay0[i])) +
				(f*g10)*x2 +
				(f*g11)*(x1+x3) +
				(f*g12)*(x0+x4)
			samples[frameOffset+i] = sum
			x4 = x3
			x3 = x2
			x2 = x1
			x1 = x0
		}
	} else if windowSq != nil {
		windowSqView := windowSq[:overlap]
		for ; i < overlap; i++ {
			f := windowSqView[i]
			oneMinus := float32(1.0) - f
			x0 := combPlanarAtFloat32(samples, hist, history, base1+i+4)
			sum := samples[frameOffset+i] +
				(oneMinus*g00)*combPlanarAtFloat32(samples, hist, history, base0+i+2) +
				(oneMinus*g01)*(combPlanarAtFloat32(samples, hist, history, base0+i+3)+combPlanarAtFloat32(samples, hist, history, base0+i+1)) +
				(oneMinus*g02)*(combPlanarAtFloat32(samples, hist, history, base0+i+4)+combPlanarAtFloat32(samples, hist, history, base0+i)) +
				(f*g10)*x2 +
				(f*g11)*(x1+x3) +
				(f*g12)*(x0+x4)
			samples[frameOffset+i] = sum
			x4 = x3
			x3 = x2
			x2 = x1
			x1 = x0
		}
	} else {
		windowView := window[:overlap]
		for ; i < overlap; i++ {
			w := windowView[i]
			f := w * w
			oneMinus := float32(1.0) - f
			x0 := combPlanarAtFloat32(samples, hist, history, base1+i+4)
			sum := samples[frameOffset+i] +
				(oneMinus*g00)*combPlanarAtFloat32(samples, hist, history, base0+i+2) +
				(oneMinus*g01)*(combPlanarAtFloat32(samples, hist, history, base0+i+3)+combPlanarAtFloat32(samples, hist, history, base0+i+1)) +
				(oneMinus*g02)*(combPlanarAtFloat32(samples, hist, history, base0+i+4)+combPlanarAtFloat32(samples, hist, history, base0+i)) +
				(f*g10)*x2 +
				(f*g11)*(x1+x3) +
				(f*g12)*(x0+x4)
			samples[frameOffset+i] = sum
			x4 = x3
			x3 = x2
			x2 = x1
			x1 = x0
		}
	}

	if g1 == 0 {
		return
	}

	x4 = combPlanarAtFloat32(samples, hist, history, base1+i)
	x3 = combPlanarAtFloat32(samples, hist, history, base1+i+1)
	x2 = combPlanarAtFloat32(samples, hist, history, base1+i+2)
	x1 = combPlanarAtFloat32(samples, hist, history, base1+i+3)
	histEnd := t1 - frameOffset - 2
	histLimit := histEnd
	if histLimit > n {
		histLimit = n
	}
	if i < histLimit {
		dst := samples[frameOffset+i : frameOffset+histLimit]
		delay := hist[base1+i+4 : base1+histLimit+4]
		x4, x3, x2, x1 = combFilterConstFloat32Hist(dst, delay, g10, g11, g12, x4, x3, x2, x1)
		i = histLimit
	}
	if i < n {
		dst := samples[frameOffset+i : frameOffset+n]
		delay := samples[frameOffset-t1+i+2 : frameOffset-t1+n+2]
		combFilterConstFloat32(dst, delay, g10, g11, g12, x4, x3, x2, x1)
	}
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
	x1 := float32(delay1[3])
	x2 := float32(delay1[2])
	x3 := float32(delay1[1])
	x4 := float32(delay1[0])
	var delay0 []celtSig

	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	} else if overlap > 0 {
		delay0 = src[start-t0-2:]
	}

	i := 0
	for ; i < overlap; i++ {
		w := window[i]
		f := noFMA32Mul(w, w)
		oneMinus := float32(1.0) - f
		x0 := float32(delay1[i+4])
		sum := float32(srcFrame[i]) +
			(oneMinus*g00)*float32(delay0[i+2]) +
			(oneMinus*g01)*(float32(delay0[i+3])+float32(delay0[i+1])) +
			(oneMinus*g02)*(float32(delay0[i+4])+float32(delay0[i])) +
			(f*g10)*x2 +
			(f*g11)*(x1+x3) +
			(f*g12)*(x0+x4)
		dstFrame[i] = celtSig(sum)

		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}

	if g1 == 0 {
		if i < n {
			copy(dstFrame[i:n], srcFrame[i:n])
		}
		return
	}

	x4 = float32(delay1[i])
	x3 = float32(delay1[i+1])
	x2 = float32(delay1[i+2])
	x1 = float32(delay1[i+3])
	for ; i < n; i++ {
		x0 := float32(delay1[i+4])
		dstFrame[i] = celtSig(combFilterConstValue(float32(srcFrame[i]), g10, g11, g12, x2, x1, x3, x0, x4))

		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}

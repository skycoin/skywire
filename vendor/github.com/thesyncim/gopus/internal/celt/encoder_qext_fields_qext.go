//go:build gopus_qext

package celt

type encoderQEXTFields struct {
	qext *encoderQEXTState
}

type encoderQEXTScratchFields struct {
	qext *encoderQEXTScratch
}

func (e *Encoder) ensureQEXTState() *encoderQEXTState {
	if e.qext == nil {
		e.qext = &encoderQEXTState{}
	}
	return e.qext
}

func (e *Encoder) qextActive() bool {
	return e.qext != nil && e.qext.enabled
}

func (e *Encoder) clearLastQEXTPayload() {
	if e.qext != nil {
		e.qext.lastPayload = e.qext.lastPayload[:0]
	}
}

func (e *Encoder) resetQEXTEnergyHistory() {
	if e.qext != nil {
		clear(e.qext.oldBandE)
	}
}

func (e *Encoder) ensureQEXTOldBandE(channels int) []celtGLog {
	state := e.ensureQEXTState()
	n := MaxBands * channels
	if len(state.oldBandE) < n {
		state.oldBandE = ensureGLogSliceNoClear(&state.oldBandE, n)
	}
	return state.oldBandE[:n]
}

func (e *Encoder) setLastQEXTPayload(payload []byte) {
	e.ensureQEXTState().lastPayload = payload
}

func (e *Encoder) lastQEXTPayloadNonEmpty() bool {
	return e.qext != nil && len(e.qext.lastPayload) > 0
}

func (s *encoderScratch) ensureQEXTScratch() *encoderQEXTScratch {
	if s.qext == nil {
		s.qext = &encoderQEXTScratch{}
	}
	return s.qext
}

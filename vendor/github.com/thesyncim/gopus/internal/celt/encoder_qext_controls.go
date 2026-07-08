//go:build gopus_qext

package celt

// SetQEXTEnabled toggles the internal extended-precision side payload path.
func (e *Encoder) SetQEXTEnabled(enabled bool) {
	state := e.ensureQEXTState()
	state.enabled = enabled
	if !enabled {
		state.lastPayload = state.lastPayload[:0]
	}
}

// QEXTEnabled reports whether the internal QEXT side path is enabled.
func (e *Encoder) QEXTEnabled() bool {
	return e.qext != nil && e.qext.enabled
}

// LastQEXTPayload returns the retained QEXT side payload from the last frame.
func (e *Encoder) LastQEXTPayload() []byte {
	if e.qext == nil {
		return nil
	}
	return e.qext.lastPayload
}

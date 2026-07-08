//go:build gopus_qext

package encoder

// SetQEXT toggles the internal libopus-style CELT QEXT encoder path.
func (e *Encoder) SetQEXT(enabled bool) {
	e.qextEnabled = enabled
	if e.qextEnabled {
		e.ensureExtensionPacketScratch()
	}
	if e.celtEncoder != nil {
		e.celtEncoder.SetQEXTEnabled(e.qextEnabled)
	}
}

// QEXT reports whether the internal CELT QEXT path is enabled.
func (e *Encoder) QEXT() bool {
	return e.qextEnabled
}

func (e *Encoder) syncQEXTToCELT() {
	if e.celtEncoder != nil {
		e.celtEncoder.SetQEXTEnabled(e.qextEnabled)
	}
}

func (e *Encoder) lastQEXTPayload() []byte {
	if e.celtEncoder == nil {
		return nil
	}
	return e.celtEncoder.LastQEXTPayload()
}

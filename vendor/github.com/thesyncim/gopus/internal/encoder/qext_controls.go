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

func (e *Encoder) maxOutputPacketBytes() int {
	capBytes := libopusMaxDataBytesCap
	if e.qextActive() {
		capBytes = hd96kQEXTPacketSizeCap
	}
	return capBytes * 6
}

func (e *Encoder) setCELTQEXTEnabled(enabled bool) {
	if e.celtEncoder != nil {
		e.celtEncoder.SetQEXTEnabled(enabled)
	}
}

func (e *Encoder) lastQEXTPayload() []byte {
	if payload, used := e.fixedQEXTPayloadIfUsed(); used {
		return payload
	}
	if e.celtEncoder == nil {
		return nil
	}
	return e.celtEncoder.LastQEXTPayload()
}

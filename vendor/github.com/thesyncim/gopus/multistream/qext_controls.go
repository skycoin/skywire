//go:build gopus_qext

package multistream

// SetQEXT toggles the optional CELT QEXT path for all stream encoders. This
// method is available in builds tagged gopus_qext.
func (e *Encoder) SetQEXT(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetQEXT(enabled)
	}
}

// QEXT reports whether the optional CELT QEXT path is enabled. This method is
// available in builds tagged gopus_qext.
func (e *Encoder) QEXT() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].QEXT()
	}
	return false
}

//go:build gopus_qext

package gopus

// SetQEXT enables or disables QEXT packet extensions on pure CELT frames. It
// is available only in builds tagged gopus_qext and defaults to disabled. The
// build tag also enables the native 96 kHz API; this control independently
// selects QEXT payload generation.
func (e *Encoder) SetQEXT(enabled bool) error {
	e.enc.SetQEXT(enabled)
	return nil
}

// QEXT reports whether QEXT payload generation is enabled. It does not report
// whether a particular packet contains a QEXT payload.
func (e *Encoder) QEXT() (bool, error) {
	return e.enc.QEXT(), nil
}

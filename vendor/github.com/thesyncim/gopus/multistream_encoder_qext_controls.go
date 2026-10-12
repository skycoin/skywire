//go:build gopus_qext

package gopus

// SetQEXT toggles the libopus ENABLE_QEXT encoder extension. This method is
// available in builds tagged gopus_qext.
func (e *MultistreamEncoder) SetQEXT(enabled bool) error {
	e.enc.SetQEXT(enabled)
	return nil
}

// QEXT reports whether the optional CELT QEXT encoder extension is enabled.
// This method is available in builds tagged gopus_qext.
func (e *MultistreamEncoder) QEXT() (bool, error) {
	return e.enc.QEXT(), nil
}

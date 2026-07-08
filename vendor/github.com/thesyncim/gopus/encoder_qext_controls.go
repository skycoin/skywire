//go:build gopus_qext

package gopus

// SetQEXT toggles the libopus ENABLE_QEXT encoder extension.
func (e *Encoder) SetQEXT(enabled bool) error {
	e.enc.SetQEXT(enabled)
	return nil
}

// QEXT reports whether the optional extended-precision theta path is enabled.
func (e *Encoder) QEXT() (bool, error) {
	return e.enc.QEXT(), nil
}

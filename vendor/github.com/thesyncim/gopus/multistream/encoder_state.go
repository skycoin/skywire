package multistream

// AnyStreamHasCodedFrame reports whether any child encoder has committed a
// frame since it was created or reset. libopus rejects an application change
// once the affected child encoder has committed a frame.
func (e *Encoder) AnyStreamHasCodedFrame() bool {
	for _, child := range e.encoders {
		if child.FirstFrameCoded() {
			return true
		}
	}
	return false
}

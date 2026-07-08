package celt

// TestEncodeLaplace exposes encodeLaplace for testing.
//
// This helper exists for tests and codec-development tooling and may change.
func (e *Encoder) TestEncodeLaplace(val, fs, decay int) int {
	return e.encodeLaplace(val, fs, decay)
}

// SetFrameBitsForTest exposes frameBits for testing.
func (e *Encoder) SetFrameBitsForTest(bits int) {
	e.frameBits = int32(bits)
}

// TestDecodeLaplace is defined in export_test.go

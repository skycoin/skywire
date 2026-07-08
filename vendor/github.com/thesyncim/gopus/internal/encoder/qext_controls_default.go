//go:build !gopus_qext

package encoder

func (e *Encoder) syncQEXTToCELT() {}

func (e *Encoder) lastQEXTPayload() []byte {
	return nil
}

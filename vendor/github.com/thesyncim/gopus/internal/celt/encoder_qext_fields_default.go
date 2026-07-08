//go:build !gopus_qext

package celt

type encoderQEXTFields struct{}

type encoderQEXTScratchFields struct{}

func (e *Encoder) ensureQEXTState() *encoderQEXTState {
	return nil
}

func (e *Encoder) qextActive() bool {
	return false
}

func (e *Encoder) clearLastQEXTPayload() {}

func (e *Encoder) setLastQEXTPayload(_ []byte) {}

func (e *Encoder) lastQEXTPayloadNonEmpty() bool {
	return false
}

func (s *encoderScratch) ensureQEXTScratch() *encoderQEXTScratch {
	return nil
}

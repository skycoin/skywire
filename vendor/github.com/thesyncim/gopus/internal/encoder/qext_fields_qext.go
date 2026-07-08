//go:build gopus_qext

package encoder

type encoderQEXTFields struct {
	// qextEnabled mirrors libopus OPUS_SET_QEXT and is applied lazily to CELT.
	qextEnabled bool
}

func (e *Encoder) qextActive() bool {
	return e.qextEnabled
}

//go:build !gopus_qext

package encoder

type encoderQEXTFields struct{}

func (e *Encoder) qextActive() bool {
	return false
}

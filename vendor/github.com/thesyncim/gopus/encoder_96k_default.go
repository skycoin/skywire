//go:build !gopus_qext

package gopus

import "github.com/thesyncim/gopus/internal/encoder"

type encoderHD96kFields struct{}

func (e *Encoder) is96kHz() bool { return false }

func (e *Encoder) apiFrameSize() int { return int(e.frameSize) }

func (e *Encoder) tryEncodeNative96k(_ []float32, _ []byte, _ encoder.EncodeInputFormat) (int, bool, error) {
	return 0, false, nil
}

func init96kEncoder(_ *Encoder) {}

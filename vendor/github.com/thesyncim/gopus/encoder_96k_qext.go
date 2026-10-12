//go:build gopus_qext

package gopus

import "github.com/thesyncim/gopus/internal/encoder"

type encoderHD96kFields struct {
	apiIs96kHz bool
}

func (e *Encoder) is96kHz() bool { return e.apiIs96kHz }

// apiFrameSize returns the frame size in API-rate samples. At 96 kHz,
// e.frameSize retains the equivalent 48 kHz count used by the public controls.
func (e *Encoder) apiFrameSize() int {
	if e.apiIs96kHz {
		return int(e.frameSize) * 2
	}
	return int(e.frameSize)
}

func (e *Encoder) tryEncodeNative96k(pcm []float32, data []byte, input encoder.EncodeInputFormat) (int, bool, error) {
	frameSize := e.apiFrameSize()
	if len(pcm) != frameSize*int(e.channels) {
		return 0, true, ErrInvalidFrameSize
	}
	frameSize, err := selectExpertFrameSize(frameSize, e.expertFrameDuration, e.application, 96000)
	e.enc.BeginEncodeCall(input, frameSize)
	if err != nil {
		return 0, true, err
	}
	if len(data) == 0 {
		return 0, true, ErrBufferTooSmall
	}
	encodePCM := pcm[:frameSize*int(e.channels)]
	packet, err := e.enc.EncodeFloat32WithAnalysisMaxBytes(encodePCM, frameSize, encodePCM, len(data))
	if err != nil {
		return 0, true, err
	}
	n, err := copyEncodedPacket(packet, data)
	return n, true, err
}

func init96kEncoder(e *Encoder) {
	e.apiIs96kHz = true
}

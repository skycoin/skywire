package gopus

import "github.com/thesyncim/gopus/internal/opusmath"

func packetFrameCount(data []byte) (TOC, int, error) {
	if len(data) < 1 {
		return TOC{}, 0, ErrPacketTooShort
	}
	toc := ParseTOC(data[0])
	switch toc.FrameCode {
	case 0:
		return toc, 1, nil
	case 1, 2:
		return toc, 2, nil
	case 3:
		if len(data) < 2 {
			return TOC{}, 0, ErrPacketTooShort
		}
		m := int(data[1] & 0x3F)
		if m == 0 || m > 48 {
			return TOC{}, 0, ErrInvalidFrameCount
		}
		if toc.FrameSize*m > maxRepacketizerDuration48k {
			return TOC{}, 0, ErrInvalidPacket
		}
		return toc, m, nil
	default:
		return TOC{}, 0, ErrInvalidPacket
	}
}

func decodeGainLinear(gainQ8 int) float32 {
	return opusmath.CeltExp2(float32(6.48814081e-4) * float32(gainQ8))
}

func (d *Decoder) applyOutputGain(samples []float32) {
	if d.decodeGainQ8 == 0 || len(samples) == 0 {
		return
	}
	g := decodeGainLinear(d.decodeGainQ8)
	for i := range samples {
		samples[i] *= g
	}
}

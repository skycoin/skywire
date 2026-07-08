package dred

import "github.com/thesyncim/gopus/internal/rangecoding"

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func decodeLaplaceP0(rd *rangecoding.Decoder, p0, decay uint16) int {
	signICDF := [3]uint16{32768 - p0, (32768 - p0) / 2, 0}
	s := rd.DecodeICDF16(signICDF[:], 15)
	if s == 2 {
		s = -1
	}
	if s == 0 {
		return 0
	}

	icdf := [8]uint16{}
	icdf[0] = uint16(maxInt(7, int(decay)))
	for i := 1; i < 7; i++ {
		icdf[i] = uint16(maxInt(7-i, int((uint32(icdf[i-1])*uint32(decay))>>15)))
	}

	value := 1
	for {
		v := rd.DecodeICDF16(icdf[:], 15)
		value += v
		if v != 7 {
			return s * value
		}
	}
}

func decodeDREDLatents(rd *rangecoding.Decoder, p0Table, rTable []uint8) {
	for i := range p0Table {
		if rTable[i] == 0 || p0Table[i] == 255 {
			continue
		}
		_ = decodeLaplaceP0(rd, uint16(p0Table[i])<<7, uint16(rTable[i])<<7)
	}
}

func parseHeaderWithDecoder(payload []byte, dredFrameOffset int, rd *rangecoding.Decoder) (Header, error) {
	if len(payload) == 0 {
		return Header{}, errInvalidHeader
	}

	rd.Init(payload)

	q0 := int32(rd.DecodeUniform(16))
	dq := int32(rd.DecodeUniform(8))

	extraOffset := int32(0)
	if rd.DecodeUniform(2) != 0 {
		extraOffset = 32 * int32(rd.DecodeUniform(256))
	}

	dredOffset := 16 - int32(rd.DecodeUniform(32)) - extraOffset + int32(dredFrameOffset)
	qmax := int32(15)
	if q0 < 14 && dq > 0 {
		nvals := 15 - (q0 + 1)
		s := int32(rd.Decode(uint32(2 * nvals)))
		if s >= nvals {
			qmax = q0 + (s - nvals) + 1
			rd.Update(uint32(s), uint32(s+1), uint32(2*nvals))
		} else {
			rd.Update(0, uint32(nvals), uint32(2*nvals))
		}
	}

	return Header{
		Q0:              q0,
		DQ:              dq,
		QMax:            qmax,
		ExtraOffset:     extraOffset,
		DredOffset:      dredOffset,
		DredFrameOffset: int32(dredFrameOffset),
	}, nil
}

func payloadLatents(payload []byte, header Header, rd *rangecoding.Decoder) int {
	stateOffset := int(header.Q0) * StateDim
	decodeDREDLatents(rd, dredStateP0Q8[stateOffset:stateOffset+StateDim], dredStateRQ8[stateOffset:stateOffset+StateDim])

	latents := 0
	for i := 0; i < NumRedundancyFrames; i += 2 {
		if len(payload)*8-rd.Tell() <= 7 {
			break
		}
		quant := header.QuantizerLevel(i / 2)
		offset := int(quant) * LatentDim
		decodeDREDLatents(rd, dredLatentP0Q8[offset:offset+LatentDim], dredLatentRQ8[offset:offset+LatentDim])
		latents++
	}
	return latents
}

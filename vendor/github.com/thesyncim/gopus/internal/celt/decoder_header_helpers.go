package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

type decodedFrameHeader struct {
	postfilterGain   float32
	postfilterPeriod int
	postfilterTapset int
	shortBlocks      int
	transient        bool
	intra            bool
}

func (d *Decoder) decodeFrameHeader(rd *rangecoding.Decoder, totalBits, frameSize, start, end, lm, transientShortBlocks int) decodedFrameHeader {
	header := decodedFrameHeader{
		shortBlocks: 1,
	}

	tell := rd.Tell()
	if start == 0 && tell+16 <= totalBits {
		if rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniformSmall(6))
			header.postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			qg := int(rd.DecodeRawBits(3))
			if rd.Tell()+2 <= totalBits {
				header.postfilterTapset = rd.DecodeICDF(tapsetICDF, 2)
			}
			header.postfilterGain = float32(0.09375) * float32(qg+1)
		}
		tell = rd.Tell()
	}

	if lm > 0 && tell+3 <= totalBits {
		header.transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	if tell+3 <= totalBits {
		header.intra = rd.DecodeBit(3) == 1
	}

	d.applyLossEnergySafety(header.intra, start, end, lm)

	if header.transient {
		header.shortBlocks = transientShortBlocks
	}

	return header
}

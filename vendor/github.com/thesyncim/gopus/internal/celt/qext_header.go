package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

type qextHeader struct {
	EndBands   int
	Intensity  int
	DualStereo bool
}

// encodeQEXTHeader mirrors the leading libopus QEXT prefix: the wide-band flag
// followed by optional stereo params. The coarse-energy intra flag is emitted
// by quant_coarse_energy() immediately after this prefix, not here.
func encodeQEXTHeader(enc *rangecoding.Encoder, channels int, hdr qextHeader) {
	if enc == nil {
		return
	}

	wide := hdr.EndBands == nbQEXTBands
	enc.EncodeBit(boolToInt(wide), 1)

	if channels == 2 {
		intensity := max(hdr.Intensity, 0)
		if intensity > hdr.EndBands {
			intensity = hdr.EndBands
		}
		enc.EncodeUniform(uint32(intensity), uint32(hdr.EndBands+1))
		if intensity != 0 {
			enc.EncodeBit(boolToInt(hdr.DualStereo), 1)
		}
	}
}

// decodeQEXTHeader mirrors libopus decode-side parsing of the QEXT prefix. The
// first coarse-energy intra bit is not consumed here.
func decodeQEXTHeader(dec *rangecoding.Decoder, channels int, totalBytes int) qextHeader {
	_ = totalBytes
	hdr := qextHeader{EndBands: 2}
	if dec == nil {
		return hdr
	}

	if dec.DecodeBit(1) == 1 {
		hdr.EndBands = nbQEXTBands
	}

	if channels == 2 {
		hdr.Intensity = int(dec.DecodeUniform(uint32(hdr.EndBands + 1)))
		if hdr.Intensity != 0 {
			hdr.DualStereo = dec.DecodeBit(1) == 1
		}
	}
	return hdr
}

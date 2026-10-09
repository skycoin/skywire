package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// DecodeStereoParams decodes stereo parameters (intensity and dual stereo).
// Reference: RFC 6716 Section 4.3.4, libopus celt/celt_decoder.c
func (d *Decoder) DecodeStereoParams(nbBands int) (intensity, dualStereo int) {
	if d.rangeDecoder == nil {
		return -1, 0
	}

	// IntensityDecay = 16384 (Q15)
	const decay = 16384

	// Compute fs0 exactly as encoder does
	// fs0 = laplaceNMin + (laplaceFS - laplaceNMin)*decay >> 15
	fs0 := laplaceNMin + ((laplaceFS-laplaceNMin)*decay)>>15

	// Decode intensity band index using Laplace distribution
	intensity = d.decodeLaplace(fs0, decay)

	// Decode dual stereo flag
	dualStereo = d.rangeDecoder.DecodeBit(1)

	return intensity, dualStereo
}

// decodeSilenceFlag reads the CELT silence flag as celt_decode_with_ec() does
// and, for a silent frame, pretends the remaining bits were read
// (dec->nbits_total += tell - ec_tell(dec)). A silent frame then runs every
// later decode stage against the exhausted budget, exactly like libopus.
func decodeSilenceFlag(rd *rangecoding.Decoder, totalBits int) bool {
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		rd.SkipToTell(totalBits)
	}
	return silence
}

// applyDecodedSilence mirrors the silence handling of celt_decode_with_ec()
// just before celt_synthesis(): oldBandE is set to -28 for every coded band,
// and denormalise_bands(silence=1) produces an all-zero spectrum, including
// QEXT bands, which synthesis overlap-adds against the carried decode memory.
func applyDecodedSilence(energies []celtGLog, coeffsL, coeffsR []celtNorm, qext *preparedQEXTDecode) {
	for i := range energies {
		energies[i] = -28.0
	}
	clear(coeffsL)
	clear(coeffsR)
	if qext != nil {
		clear(qext.coeffsL)
		clear(qext.coeffsR)
	}
}

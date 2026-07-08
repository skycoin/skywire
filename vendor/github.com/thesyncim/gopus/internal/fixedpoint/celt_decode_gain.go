//go:build gopus_fixed_point

package fixedpoint

// DecodeGainQ16 computes the linear output gain applied by opus_decode_frame
// when st->decode_gain != 0, in the FIXED_POINT path:
//
//	gain = celt_exp2(MULT16_16_P15(QCONST16(6.48814081e-4f, 25), st->decode_gain));
//
// QCONST16(6.48814081e-4f, 25) = (opus_val16)(0.5 + 6.48814081e-4*(1<<25)) = 21772.
// MULT16_16_P15(a,b) = SHR(ADD32(16384, MULT16_16(a,b)), 15), with a,b taken as
// int16 (decode_gain is in [-32768, 32767]). The MULT16_16_P15 result is the Q10
// exponent fed to celt_exp2, whose output is Q16.
func DecodeGainQ16(decodeGain int) int32 {
	const qconst = 21772 // QCONST16(6.48814081e-4f, 25)
	// MULT16_16 treats both operands as int16; reproduce that truncation.
	a := int32(int16(qconst))
	b := int32(int16(decodeGain))
	// MULT16_16_P15: (16384 + a*b) >> 15. a*b fits int32 (|a*b| <= 21772*32768).
	exp := int16((16384 + a*b) >> 15)
	return CeltExp2(exp)
}

// ApplyDecodeGainRes applies the FIXED_POINT decode gain in place to an
// interleaved opus_res buffer, mirroring opus_decode_frame (ENABLE_RES24):
//
//	x = MULT32_32_Q16(pcm[i], gain);
//	pcm[i] = SATURATE(x, 32767);
//
// MULT32_32_Q16(a,b) = SHR((opus_int64)a*(opus_int64)b, 16). SATURATE(x,32767)
// clamps to [-32767, 32767]. gain is the Q16 value from DecodeGainQ16.
func ApplyDecodeGainRes(pcm []int32, gain int32) {
	g := int64(gain)
	for i := range pcm {
		x := int32((int64(pcm[i]) * g) >> 16)
		if x > 32767 {
			x = 32767
		} else if x < -32767 {
			x = -32767
		}
		pcm[i] = x
	}
}

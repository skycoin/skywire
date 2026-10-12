package gopus

import "github.com/thesyncim/gopus/internal/opusmath"

func float32ToInt16(sample float32) int16 {
	return opusFloatToInt16(sample)
}

func opusFloatToInt16(sample float32) int16 {
	return opusmath.Float32ToInt16(sample)
}

// float32ToInt24 converts float32 PCM to a 24-bit-scale integer stored in
// int32, matching the libopus RES2INT24 macro for float builds:
//
//	RES2INT24(a) = float2int(32768.f * 256.f * (a))   (arch.h, float build)
//
// The scale is 2^23. The conversion does not soft-clip or saturate: +1.0 maps
// to 8388608, and values can exceed the nominal signed 24-bit range.
func float32ToInt24(sample float32) int32 {
	return opusmath.Float32ToInt24(sample)
}

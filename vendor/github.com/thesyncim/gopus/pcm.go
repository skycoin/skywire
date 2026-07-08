package gopus

import "github.com/thesyncim/gopus/internal/opusmath"

func float32ToInt16(sample float32) int16 {
	return opusFloatToInt16(sample)
}

func opusFloatToInt16(sample float32) int16 {
	return opusmath.Float32ToInt16(sample)
}

// float32ToInt24 converts a float32 PCM sample to a signed 24-bit integer
// stored in int32, matching the libopus RES2INT24 macro for float builds:
//
//	RES2INT24(a) = float2int(32768.f * 256.f * (a))   (arch.h, float build)
//
// Full scale is ±8388608 (= 2^23). No saturation is applied; callers must
// ensure the input has been soft-clipped to [-1, 1] beforehand.
func float32ToInt24(sample float32) int32 {
	return opusmath.Float32ToInt24(sample)
}

package opusmath

// Float32ToInt16 converts a normalised float sample in roughly [-1, 1) to signed
// 16-bit PCM, matching libopus FLOAT2INT16() (celt/float_cast.h): scale by 32768
// then round-half-to-even and saturate to the int16 rails. The scale is applied
// in float32 first (as the C macro does) before the rounding/clamp in
// [Float32ToInt16Raw], so the result is bit-exact with the reference.
func Float32ToInt16(x float32) int16 {
	y := x * 32768.0
	return Float32ToInt16Raw(y)
}

// Float32ToInt24 converts a float32 PCM sample to a signed 24-bit integer
// stored in int32, matching libopus RES2INT24 for float builds:
//
//	RES2INT24(a) = float2int(32768.f * 256.f * (a))  (celt/arch.h)
//
// Full scale is ±8388608 (= 2^23). No explicit saturation is applied;
// libopus does not soft-clip before this conversion in the 24-bit path.
func Float32ToInt24(x float32) int32 {
	return roundFloat32ToInt32Even(x * 8388608.0)
}

// Float32ToInt16Raw rounds and saturates an already-scaled value (i.e. one in
// 16-bit code units, not normalised [-1, 1)) to int16. It is the inner half of
// FLOAT2INT16 and of silk_float2short(): clamp to [-32768, 32767] then
// round-half-to-even. The full negative rail (-32768) is allowed here, unlike
// the OSCE variant. Comparisons are done in float32 to match the C order of
// operations.
func Float32ToInt16Raw(y float32) int16 {
	if y > 32767.0 {
		return 32767
	}
	if y < -32768.0 {
		return -32768
	}
	return int16(roundClampedFloat32ToInt32Even(y))
}

// Float32ToInt16OSCEOutputScale mirrors libopus OSCE SCALE_OUTPUT quantization.
// Unlike the generic PCM helper it clamps the negative rail to -32767 (a
// symmetric +/-32767 range), matching the OSCE output scaling in the DNN
// post-filters; the scale-by-32768 and round-half-to-even are otherwise the
// same as [Float32ToInt16].
func Float32ToInt16OSCEOutputScale(x float32) int16 {
	y := x * 32768.0
	if y > 32767.0 {
		return 32767
	}
	if y < -32767.0 {
		return -32767
	}
	return int16(roundClampedFloat32ToInt32Even(y))
}

// roundHalfEvenMagic32 is 1.5 * 2^23. Adding it to a float32 y with |y| < 2^22
// forces the sum into [2^23, 2^24), where the float32 ULP is exactly 1.0, so
// the IEEE round-to-nearest-even addition rounds y to an integer; subtracting
// the constant back recovers that integer exactly.
const roundHalfEvenMagic32 = float32(12582912.0)

// roundClampedFloat32ToInt32Even is the branchless round-to-nearest-even used
// on already-clamped 16-bit-scale values (|y| <= 32768). It is bit-exact with
// [roundFloat32ToInt32Even] for |y| < 2^22 but avoids that helper's
// data-dependent branches, which mispredict on roughly every other audio
// sample.
func roundClampedFloat32ToInt32Even(y float32) int32 {
	return int32((y + roundHalfEvenMagic32) - roundHalfEvenMagic32)
}

// roundFloat32ToInt32Even rounds y to the nearest integer with ties to even,
// reproducing the float2int()/lrintf() round-to-nearest-even behaviour libopus
// relies on under the default IEEE rounding mode. It truncates toward zero, then
// adjusts by inspecting the float32 fractional remainder: a half-way fraction
// only rounds away from the truncated value when that value is odd. The fraction
// is computed in float32 so the exact-0.5 tie test matches the C result.
func roundFloat32ToInt32Even(y float32) int32 {
	i := int32(y)
	frac := y - float32(i)
	if frac > 0.5 || (frac == 0.5 && (i&1) != 0) {
		i++
	} else if frac < -0.5 || (frac == -0.5 && (i&1) != 0) {
		i--
	}
	return i
}

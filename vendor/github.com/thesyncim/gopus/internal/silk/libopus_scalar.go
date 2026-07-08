package silk

import "github.com/thesyncim/gopus/internal/opusmath"

type silkCReal = opusmath.CReal

func roundF32HalfAwayFromZeroToInt32(x float32) int32 {
	return opusmath.RoundF32HalfAwayFromZeroToInt32(x)
}

func expF32(x float32) float32 {
	return opusmath.ExpF32(x)
}

func exp2F32(x float32) float32 {
	return opusmath.Exp2F32(x)
}

func exp2DivIntF32(x float32, denom int) float32 {
	return opusmath.Exp2DivIntF32(x, denom)
}

func silkLog2F32(x float32) float32 {
	return opusmath.SilkLog2F32(x)
}

func sqrtF32(x float32) float32 {
	return opusmath.SqrtF32(x)
}

func floorHalfPlusF32ToInt32(x float32) int32 {
	return opusmath.FloorHalfPlusF32ToInt32(x)
}

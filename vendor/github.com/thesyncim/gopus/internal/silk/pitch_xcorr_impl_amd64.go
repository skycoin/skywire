//go:build amd64 && !purego

package silk

import "github.com/thesyncim/gopus/internal/cpufeat"

var silkUsePitchXcorrAVX2FMA = cpufeat.AMD64.HasAVX2 && cpufeat.AMD64.HasFMA

//go:noescape
func xcorrKernelAVX8(x, y *float32, sum *[8]float32, length int)

func celtPitchXcorrFloatImpl(x, y []float32, out []float32, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	if !silkUsePitchXcorrAVX2FMA {
		celtPitchXcorrFloatImplScalar(x, y, out, length, maxPitch)
		return
	}

	_ = x[length-1]
	_ = y[maxPitch+length-2]
	_ = out[maxPitch-1]

	i := 0
	for ; i < maxPitch-7; i += 8 {
		var sums [8]float32
		xcorrKernelAVX8(&x[0], &y[i], &sums, length)
		copy(out[i:i+8], sums[:])
	}
	for ; i < maxPitch; i++ {
		out[i] = innerProductF32Acc(x, y[i:], length)
	}
}

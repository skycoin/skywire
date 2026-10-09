//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"

	"github.com/thesyncim/gopus/internal/opusmath"
)

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
	// Clear the upper register halves the 256-bit kernel leaves dirty before
	// the scalar SSE code that follows.
	archsimd.ClearAVXUpperBits()
	// celt_pitch_xcorr_avx2 finishes the last maxPitch%8 lags with
	// celt_inner_prod(), which the x86 SIMD build dispatches to
	// celt_inner_prod_sse.
	for ; i < maxPitch; i++ {
		out[i] = innerProductF32SSEOrder(x, y[i:], length)
	}
}

// innerProductF32SSEOrder reproduces libopus x86/pitch_sse.c
// celt_inner_prod_sse: one 4-lane MULPS/ADDPS accumulator, the
// (a0+a2)+(a1+a3) reduction, and the target's MAC16_16 scalar tail.
func innerProductF32SSEOrder(x, y []float32, length int) float32 {
	x = x[:length]
	y = y[:length]
	xp := unsafe.Pointer(unsafe.SliceData(x))
	yp := unsafe.Pointer(unsafe.SliceData(y))
	var acc archsimd.Float32x4
	i := 0
	for ; i+4 <= length; i += 4 {
		ax := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(xp, i*4)))
		ay := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(yp, i*4)))
		acc = acc.Add(ax.Mul(ay))
	}
	sum := (acc.GetElem(0) + acc.GetElem(2)) + (acc.GetElem(1) + acc.GetElem(3))
	for ; i < length; i++ {
		sum = pitchXcorrMAC32(sum, x[i], y[i])
	}
	if sum != sum {
		return opusmath.PitchXcorrSSENaNReplay(x, y, length)
	}
	return sum
}

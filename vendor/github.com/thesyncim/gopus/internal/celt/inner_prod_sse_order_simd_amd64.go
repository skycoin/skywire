//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"math"
	"simd/archsimd"
	"unsafe"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// innerProdFloat32SSEOrder reproduces libopus x86/pitch_sse.c
// celt_inner_prod_sse: one 4-lane MULPS/ADDPS accumulator, the
// (a0+a2)+(a1+a3) reduction, and the target's MAC16_16 scalar tail. The
// archsimd lanes run exactly that operation sequence, so the result is
// bit-identical to innerProdFloat32SSEOrderScalar.
func innerProdFloat32SSEOrder(x, y []float32, length int) float32 {
	if length <= 0 {
		return 0
	}
	if !archsimd.X86.AVX() {
		return innerProdFloat32SSEOrderScalar(x, y, length)
	}
	x = x[:length]
	y = y[:length]
	xp := unsafe.Pointer(unsafe.SliceData(x))
	yp := unsafe.Pointer(unsafe.SliceData(y))
	var acc archsimd.Float32x4
	i := 0
	for ; i+4 <= length; i += 4 {
		off := uintptr(i) * 4
		acc = acc.Add(loadF32x4(unsafe.Add(xp, off)).Mul(loadF32x4(unsafe.Add(yp, off))))
	}
	sum := add32(add32(acc.GetElem(0), acc.GetElem(2)), add32(acc.GetElem(1), acc.GetElem(3)))
	for ; i < length; i++ {
		sum = pitchXcorrSSETailMAC32(sum, x[i], y[i])
	}
	if sum != sum {
		return opusmath.PitchXcorrSSENaNReplay(x, y, length)
	}
	return sum
}

// innerProdFloat32SSEOrderLags stores innerProdFloat32SSEOrder(x, y[l:],
// length) in xcorr[l] for every l < len(xcorr). Each lag keeps its own
// celt_inner_prod_sse accumulator and reduction; the lags' chains are
// independent, so up to five of them share one pass over x.
func innerProdFloat32SSEOrderLags(x, y, xcorr []float32, length int) {
	if length <= 0 || !archsimd.X86.AVX() {
		for l := range xcorr {
			xcorr[l] = innerProdFloat32SSEOrder(x, y[l:], length)
		}
		return
	}
	for l := 0; l < len(xcorr); l += 5 {
		innerProdFloat32SSEOrderUpTo5(x, y[l:], xcorr[l:min(l+5, len(xcorr))], length)
	}
}

// innerProdFloat32SSEOrderUpTo5 is innerProdFloat32SSEOrderLags for at most
// five lags; y must hold length+len(out)-1 samples.
func innerProdFloat32SSEOrderUpTo5(x, y, out []float32, length int) {
	n := len(out)
	if n < 5 {
		for l := range out {
			out[l] = innerProdFloat32SSEOrder(x, y[l:], length)
		}
		return
	}
	out = out[:5]
	x = x[:length]
	y = y[:length+4]
	xp := unsafe.Pointer(unsafe.SliceData(x))
	yp := unsafe.Pointer(unsafe.SliceData(y))
	var a0, a1, a2, a3, a4 archsimd.Float32x4
	i := 0
	for ; i+4 <= length; i += 4 {
		off := uintptr(i) * 4
		xv := loadF32x4(unsafe.Add(xp, off))
		yo := unsafe.Add(yp, off)
		a0 = a0.Add(xv.Mul(loadF32x4(yo)))
		a1 = a1.Add(xv.Mul(loadF32x4(unsafe.Add(yo, 4))))
		a2 = a2.Add(xv.Mul(loadF32x4(unsafe.Add(yo, 8))))
		a3 = a3.Add(xv.Mul(loadF32x4(unsafe.Add(yo, 12))))
		a4 = a4.Add(xv.Mul(loadF32x4(unsafe.Add(yo, 16))))
	}
	out[0] = innerProdSSEOrderFinish(a0, x, y, i)
	out[1] = innerProdSSEOrderFinish(a1, x, y[1:], i)
	out[2] = innerProdSSEOrderFinish(a2, x, y[2:], i)
	out[3] = innerProdSSEOrderFinish(a3, x, y[3:], i)
	out[4] = innerProdSSEOrderFinish(a4, x, y[4:], i)
}

// innerProdSSEOrderFinish completes one celt_inner_prod_sse lag from its
// four-lane accumulator a over x[:i]: the (a0+a2)+(a1+a3) reduction, the
// scalar tail over x[i:], and the NaN replay.
func innerProdSSEOrderFinish(a archsimd.Float32x4, x, y []float32, i int) float32 {
	sum := add32(add32(a.GetElem(0), a.GetElem(2)), add32(a.GetElem(1), a.GetElem(3)))
	y = y[:len(x)]
	for j := i; j < len(x); j++ {
		sum = pitchXcorrSSETailMAC32(sum, x[j], y[j])
	}
	if math.Float32bits(sum)&0x7fffffff > 0x7f800000 {
		sum = opusmath.PitchXcorrSSENaNReplay(x, y, len(x))
	}
	return sum
}

// prefilterDualInnerProdF32SSEOrder reproduces libopus x86/pitch_sse.c
// dual_inner_prod_sse. Most targets use separate vector multiply/add
// operations. The default float AMD64 v3 SIMD target selects its contracted
// implementation because GCC emits packed FMA instructions for the C
// intrinsics in that build.
func prefilterDualInnerProdF32SSEOrder(x, y1, y2 []float32, length int) (float32, float32) {
	if prefilterDualInnerProdSSEUsesFMA {
		return prefilterDualInnerProdF32SSEOrderV3(x, y1, y2, length)
	}
	if length <= 0 {
		return 0, 0
	}
	if !archsimd.X86.AVX() {
		return prefilterDualInnerProdF32SSEOrderScalar(x, y1, y2, length)
	}
	x = x[:length]
	y1 = y1[:length]
	y2 = y2[:length]
	xp := unsafe.Pointer(unsafe.SliceData(x))
	y1p := unsafe.Pointer(unsafe.SliceData(y1))
	y2p := unsafe.Pointer(unsafe.SliceData(y2))
	var acc1, acc2 archsimd.Float32x4
	i := 0
	for ; i+4 <= length; i += 4 {
		off := uintptr(i) * 4
		vx := loadF32x4(unsafe.Add(xp, off))
		acc1 = acc1.Add(vx.Mul(loadF32x4(unsafe.Add(y1p, off))))
		acc2 = acc2.Add(vx.Mul(loadF32x4(unsafe.Add(y2p, off))))
	}
	sum1 := add32(add32(acc1.GetElem(0), acc1.GetElem(2)), add32(acc1.GetElem(1), acc1.GetElem(3)))
	sum2 := add32(add32(acc2.GetElem(0), acc2.GetElem(2)), add32(acc2.GetElem(1), acc2.GetElem(3)))
	for ; i < length; i++ {
		xi := x[i]
		sum1 = add32(sum1, mul32(xi, y1[i]))
		sum2 = add32(sum2, mul32(xi, y2[i]))
	}
	return sum1, sum2
}

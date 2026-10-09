//go:build (amd64 || arm64) && goexperiment.simd && !nosimd && !purego

package celt

import (
	"github.com/thesyncim/gopus/internal/opusmath"
	"unsafe"
)

// innerProd8FMA32ArchSIMD accumulates the dot product x·y in four lanes with
// fused multiply-add (archsimd MulAdd → FMLA on arm64, VFMADD on amd64), loading
// through raw pointers (loadF32x4) to avoid the per-load slice bounds check. Lane
// L sums elements L, L+4, L+8, … exactly like the scalar reference's acc[L]. ARM
// preserves libopus NEON's multiplicand and FADD operand order; x86 keeps its
// existing source order. Four lanes is mandatory: a wider Float32x8 accumulator
// would reduce a different partial-sum tree and diverge.
//
// MulAdd lowers to the FMA instruction unconditionally, so callers must ensure the
// feature is present — always on arm64 NEON, gated on archsimd.X86.FMA() on amd64.
func innerProd8FMA32ArchSIMD(x, y []float32, n int) float32 {
	acc := broadcastF32x4Arch(0)
	if n <= 0 {
		return 0
	}
	_ = x[n-1]
	_ = y[n-1]
	xbase := unsafe.Pointer(unsafe.SliceData(x))
	ybase := unsafe.Pointer(unsafe.SliceData(y))
	i := 0
	for ; i+8 <= n; i += 8 {
		xp := unsafe.Add(xbase, i*4)
		yp := unsafe.Add(ybase, i*4)
		if libopusFloatInnerProdUsesNeonOrder {
			// celt/arm/pitch_neon_intr.c:celt_inner_prod_neon emits FMLA with
			// y as the first multiplicand and x as the second.
			acc = loadF32x4(yp).MulAdd(loadF32x4(xp), acc)
			acc = loadF32x4(unsafe.Add(yp, 16)).MulAdd(loadF32x4(unsafe.Add(xp, 16)), acc)
		} else {
			acc = loadF32x4(xp).MulAdd(loadF32x4(yp), acc)
			acc = loadF32x4(unsafe.Add(xp, 16)).MulAdd(loadF32x4(unsafe.Add(yp, 16)), acc)
		}
	}
	for ; i+4 <= n; i += 4 {
		xp := unsafe.Add(xbase, i*4)
		yp := unsafe.Add(ybase, i*4)
		if libopusFloatInnerProdUsesNeonOrder {
			acc = loadF32x4(yp).MulAdd(loadF32x4(xp), acc)
		} else {
			acc = loadF32x4(xp).MulAdd(loadF32x4(yp), acc)
		}
	}
	var sum0, sum1 float32
	if libopusFloatInnerProdUsesNeonOrder {
		// NEON reduces low+high with vadd_f32. Reversing the Go operands
		// matches the ARM FADD operand order and its NaN payload selection.
		sum0 = round32(acc.GetElem(2) + acc.GetElem(0))
		sum1 = round32(acc.GetElem(3) + acc.GetElem(1))
	} else {
		sum0 = round32(acc.GetElem(0) + acc.GetElem(2))
		sum1 = round32(acc.GetElem(1) + acc.GetElem(3))
	}
	sum := round32(sum0 + sum1)
	for ; i < n; i++ {
		xv := *(*float32)(unsafe.Add(xbase, i*4))
		yv := *(*float32)(unsafe.Add(ybase, i*4))
		if libopusFloatInnerProdUsesNeonOrder {
			// Match the ARM helper's scalar MAC16_16 tail FMADD operand slots.
			sum = opusmath.FMA32(yv, xv, sum)
		} else {
			sum = opusmath.FMA32(xv, yv, sum)
		}
	}
	return sum
}

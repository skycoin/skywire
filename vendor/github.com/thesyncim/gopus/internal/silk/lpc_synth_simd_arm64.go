//go:build arm64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

func synthesizeLPCOrder16Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	_ = A_Q12[15]
	_ = sLPC[maxLPCOrder-1]
	if subfrLength <= 0 {
		return
	}
	_ = sLPC[maxLPCOrder+subfrLength-1]
	_ = presQ14[subfrLength-1]
	_ = pxq[subfrLength-1]

	coef0 := [4]int32{int32(A_Q12[15]), int32(A_Q12[14]), int32(A_Q12[13]), int32(A_Q12[12])}
	coef1 := [4]int32{int32(A_Q12[11]), int32(A_Q12[10]), int32(A_Q12[9]), int32(A_Q12[8])}
	coef2 := [4]int32{int32(A_Q12[7]), int32(A_Q12[6]), int32(A_Q12[5]), int32(A_Q12[4])}
	coef3 := [4]int32{int32(A_Q12[3]), int32(A_Q12[2]), int32(A_Q12[1]), int32(A_Q12[0])}
	c0 := archsimd.LoadInt32x4Array(&coef0)
	c1 := archsimd.LoadInt32x4Array(&coef1)
	c2 := archsimd.LoadInt32x4Array(&coef2)
	c3 := archsimd.LoadInt32x4Array(&coef3)

	// The final checked state index is 15+subfrLength. Each loop iteration loads
	// i..i+15 and stores at 16+i, so every raw address stays inside sLPC.
	stateBase := unsafe.Pointer(unsafe.SliceData(sLPC))
	for i := 0; i < subfrLength; i++ {
		stateOffset := i * 4
		v0 := archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(stateBase, stateOffset)))
		v1 := archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(stateBase, stateOffset+16)))
		v2 := archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(stateBase, stateOffset+32)))
		v3 := archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(stateBase, stateOffset+48)))
		// The rounded products fit int32 individually. Summing them in int64
		// avoids horizontal reductions; the final narrowing preserves int32 wrap.
		predProducts := synthesizeLPCOrder16RoundProductsQ16(v0, c0)
		predProducts = predProducts.Add(synthesizeLPCOrder16RoundProductsQ16(v1, c1))
		predProducts = predProducts.Add(synthesizeLPCOrder16RoundProductsQ16(v2, c2))
		predProducts = predProducts.Add(synthesizeLPCOrder16RoundProductsQ16(v3, c3))
		pred := int32(maxLPCOrder>>1) + predProducts.TruncToInt32().ReduceSum()
		s := silkAddSat32(presQ14[i], lShiftSAT32By4(pred))
		*(*int32)(unsafe.Add(stateBase, (maxLPCOrder+i)*4)) = s
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(s, gainQ10), 8))
	}
}

func synthesizeLPCOrder16RoundProductsQ16(x, y archsimd.Int32x4) archsimd.Int64x2 {
	// Shift each product before summing to preserve silkSMLAWB's Q16 rounding.
	lo := x.MulWidenLo(y).ShiftAllRight(16)
	hi := x.HiToLo().MulWidenLo(y.HiToLo()).ShiftAllRight(16)
	return lo.Add(hi)
}

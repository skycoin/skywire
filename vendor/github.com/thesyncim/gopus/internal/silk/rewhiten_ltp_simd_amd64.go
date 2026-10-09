//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

var silkRewhitenLTPUsesAVX2 = archsimd.X86.AVX2()

// rewhitenLTP is silk_LPC_analysis_filter on the quantized history:
// sLTP[startIdx+ix] for ix in [0, length) from xq[startIdx+offset+ix]. Where
// every tap is in range it computes eight outputs per vector; the prediction
// is a wrapping int32 sum, so its lane order does not change the result.
func rewhitenLTP(sLTP []int16, xq []int16, startIdx, offset int, aQ12 []int16, length, order int) {
	// Set first 'order' outputs to zero (per libopus silk_LPC_analysis_filter)
	for i := startIdx; i < startIdx+order && i < len(sLTP); i++ {
		sLTP[i] = 0
	}
	if !silkRewhitenLTPUsesAVX2 || order <= 0 || order > maxLPCOrder || len(aQ12) < order {
		rewhitenLTPScalar(sLTP, xq, startIdx, offset, aQ12, order, length, order)
		return
	}
	rewhitenLTPAVX2(sLTP, xq, startIdx, offset, aQ12, length, order)
}

//go:noinline
func rewhitenLTPAVX2(sLTP []int16, xq []int16, startIdx, offset int, aQ12 []int16, length, order int) {
	base := startIdx + offset
	ix := order
	// Leading outputs whose taps reach before xq[0] take the guarded path.
	if head := order - base; ix < head {
		rewhitenLTPScalar(sLTP, xq, startIdx, offset, aQ12, ix, min(head, length), order)
		ix = head
	}
	if ix < order || startIdx < 0 {
		rewhitenLTPScalar(sLTP, xq, startIdx, offset, aQ12, ix, length, order)
		return
	}
	var coef [maxLPCOrder]archsimd.Int32x8
	for k := range order {
		coef[k] = archsimd.BroadcastInt32x8(int32(aQ12[k]))
	}
	half := archsimd.BroadcastInt32x8(1)
	for ; ix+8 <= length && base+ix+8 <= len(xq) && startIdx+ix+8 <= len(sLTP); ix += 8 {
		in := unsafe.Pointer(&xq[base+ix])
		var pred archsimd.Int32x8
		for k := range order {
			tap := archsimd.LoadInt16x8Array((*[8]int16)(unsafe.Add(in, -2*(k+1)))).ExtendToInt32()
			pred = pred.Add(tap.Mul(coef[k]))
		}
		outQ12 := archsimd.LoadInt16x8Array((*[8]int16)(in)).ExtendToInt32().ShiftAllLeft(12).Sub(pred)
		// silk_RSHIFT_ROUND(outQ12, 12), then silk_SAT16 in the pack.
		out := outQ12.ShiftAllRight(11).Add(half).ShiftAllRight(1)
		out.GetLo().SaturateToInt16Concat(out.GetHi()).StoreArray((*[8]int16)(unsafe.Pointer(&sLTP[startIdx+ix])))
	}
	archsimd.ClearAVXUpperBits()
	rewhitenLTPScalar(sLTP, xq, startIdx, offset, aQ12, ix, length, order)
}

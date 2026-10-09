//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

var silkLPCAnalysisFilterUsesAVX2 = archsimd.X86.AVX2()

// silkLPCAnalysisFilterVec computes silk_LPC_analysis_filter outputs eight at
// a time from index max(order, 16) on and returns the first index it did not
// compute. Each output's prediction is two VPMADDWD dot products of the 16
// preceding inputs with the (zero-padded) coefficients, summed by VPHADDD. The
// int32 sums wrap as the silk_SMLABB chain does; a VPMADDWD pair wraps only when
// both of its products are (-32768)*(-32768), which is the same wrapped sum. The
// round-and-saturate is the packing instruction's signed saturation. The
// kernel stays on 128-bit vectors, which keep the core's normal frequency.
func silkLPCAnalysisFilterVec(out, in, B []int16, length, order int) int {
	ix := order
	if !silkLPCAnalysisFilterUsesAVX2 || order <= 0 || order > maxLPCOrder || length > len(in) || length > len(out) {
		return ix
	}
	start := max(order, maxLPCOrder)
	if start+8 > length {
		return ix
	}
	return silkLPCAnalysisFilterVecAVX2(out, in, B, length, order, start)
}

//go:noinline
func silkLPCAnalysisFilterVecAVX2(out, in, B []int16, length, order, start int) int {
	ix := order
	for ; ix < start; ix++ {
		outQ12 := silkSMULBB(int32(in[ix-1]), int32(B[0]))
		for j := 1; j < order; j++ {
			outQ12 = silkSMLABB(outQ12, int32(in[ix-1-j]), int32(B[j]))
		}
		out[ix] = silkSAT16(silkRSHIFT_ROUND(silkLSHIFT(int32(in[ix]), 12)-outQ12, 12))
	}
	// rev[j] multiplies in[ix-16+j].
	var rev [maxLPCOrder]int16
	for k := range order {
		rev[maxLPCOrder-1-k] = B[k]
	}
	cLo := archsimd.LoadInt16x8Array((*[8]int16)(rev[:8]))
	cHi := archsimd.LoadInt16x8Array((*[8]int16)(rev[8:]))
	half := archsimd.BroadcastInt32x4(1)
	var zero archsimd.Int16x8
	base := unsafe.Pointer(unsafe.SliceData(in))
	pred := func(ix int) archsimd.Int32x4 {
		p := unsafe.Add(base, 2*(ix-maxLPCOrder))
		lo := archsimd.LoadInt16x8Array((*[8]int16)(p)).DotProductPairs(cLo)
		hi := archsimd.LoadInt16x8Array((*[8]int16)(unsafe.Add(p, 16))).DotProductPairs(cHi)
		return lo.Add(hi)
	}
	for ; ix+8 <= length; ix += 8 {
		p0 := pred(ix).ConcatAddPairs(pred(ix + 1)).ConcatAddPairs(pred(ix + 2).ConcatAddPairs(pred(ix + 3)))
		p1 := pred(ix + 4).ConcatAddPairs(pred(ix + 5)).ConcatAddPairs(pred(ix + 6).ConcatAddPairs(pred(ix + 7)))
		cur := archsimd.LoadInt16x8Array((*[8]int16)(unsafe.Add(base, 2*ix)))
		// in<<12 for each lane: interleaving under zeros puts in[i] in the
		// high half of lane i, so an arithmetic shift right by 4 leaves in[i]<<12.
		c0 := zero.InterleaveLo(cur).AsInt32x4().ShiftAllRight(4)
		c1 := zero.InterleaveHi(cur).AsInt32x4().ShiftAllRight(4)
		// silk_RSHIFT_ROUND(outQ12, 12), then silk_SAT16 in the pack.
		r0 := c0.Sub(p0).ShiftAllRight(11).Add(half).ShiftAllRight(1)
		r1 := c1.Sub(p1).ShiftAllRight(11).Add(half).ShiftAllRight(1)
		r0.SaturateToInt16Concat(r1).StoreArray((*[8]int16)(unsafe.Pointer(&out[ix])))
	}
	return ix
}

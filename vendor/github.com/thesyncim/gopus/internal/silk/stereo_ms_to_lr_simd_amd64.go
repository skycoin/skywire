//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

var silkStereoUsesAVX2 = archsimd.X86.AVX2()

// The stereo kernels use 128-bit vectors, as the libopus SSE decoder does,
// which keep the core's normal frequency.

// stereoSMLAWB4 is silk_SMLAWB(a, b, c) on four lanes, with c already
// sign-extended from its low 16 bits. It uses the libopus 32-bit form
// a + (b>>16)*c + (((b&0xFFFF)*c)>>16), which equals the 64-bit product form
// for every input.
func stereoSMLAWB4(a, b, c, lowMask archsimd.Int32x4) archsimd.Int32x4 {
	hi := b.ShiftAllRight(16).Mul(c)
	lo := b.And(lowMask).Mul(c).ShiftAllRight(16)
	return a.Add(hi.Add(lo))
}

// stereoLoad4 loads four int16 samples starting at p as int32 lanes.
func stereoLoad4(p unsafe.Pointer) archsimd.Int32x4 {
	return archsimd.LoadInt64x2Array(&[2]int64{*(*int64)(p), 0}).AsInt16x8().ExtendLo4ToInt32()
}

// stereoPredictSide adds the stereo prediction to side[n+1] for n in
// [from, to), advancing the Q13 predictors by delta before each sample, four
// samples per vector (libopus silk/stereo_MS_to_LR.c, which GCC vectorizes).
// Lane k of a vector starting at n uses pred + (n-from+k+1)*delta, the value
// the scalar loop's wrapping int32 additions reach.
func stereoPredictSide(mid, side []int16, from, to int, pred0, pred1, delta0, delta1 int32) {
	n := from
	if silkStereoUsesAVX2 && to+2 <= len(mid) && to+1 <= len(side) {
		n, pred0, pred1 = stereoPredictSideAVX2(mid, side, from, to, pred0, pred1, delta0, delta1)
	}
	stereoPredictSideScalar(mid, side, n, to, pred0, pred1, delta0, delta1)
}

//go:noinline
func stereoPredictSideAVX2(mid, side []int16, from, to int, pred0, pred1, delta0, delta1 int32) (int, int32, int32) {
	n := from
	steps := archsimd.LoadInt32x4Array(&[4]int32{1, 2, 3, 4})
	p0 := archsimd.BroadcastInt32x4(pred0).Add(steps.Mul(archsimd.BroadcastInt32x4(delta0)))
	p1 := archsimd.BroadcastInt32x4(pred1).Add(steps.Mul(archsimd.BroadcastInt32x4(delta1)))
	step0 := archsimd.BroadcastInt32x4(delta0 << 2)
	step1 := archsimd.BroadcastInt32x4(delta1 << 2)
	lowMask := archsimd.BroadcastInt32x4(0xFFFF)
	one := archsimd.BroadcastInt32x4(1)
	for ; n+4 <= to; n += 4 {
		m := unsafe.Pointer(&mid[n])
		m0 := stereoLoad4(m)
		m1 := stereoLoad4(unsafe.Add(m, 2))
		m2 := stereoLoad4(unsafe.Add(m, 4))
		sp := unsafe.Pointer(&side[n+1])
		s1 := stereoLoad4(sp)
		c0 := p0.ShiftAllLeft(16).ShiftAllRight(16)
		c1 := p1.ShiftAllLeft(16).ShiftAllRight(16)
		sum := m0.Add(m2).Add(m1.ShiftAllLeft(1)).ShiftAllLeft(9)
		sum = stereoSMLAWB4(s1.ShiftAllLeft(8), sum, c0, lowMask)
		sum = stereoSMLAWB4(sum, m1.ShiftAllLeft(11), c1, lowMask)
		// silk_RSHIFT_ROUND(sum, 8), then silk_SAT16 in the pack.
		res := sum.ShiftAllRight(7).Add(one).ShiftAllRight(1)
		*(*int64)(sp) = res.SaturateToInt16Concat(res).AsInt64x2().GetElem(0)
		p0 = p0.Add(step0)
		p1 = p1.Add(step1)
	}
	k := int32(n - from)
	pred0 += k * delta0
	pred1 += k * delta1
	return n, pred0, pred1
}

// stereoMidSideToLR converts mid/side to left/right in place with saturating
// 16-bit adds, L = SAT16(mid+side), R = SAT16(mid-side), eight samples per
// vector.
func stereoMidSideToLR(mid, side []int16) {
	n := 0
	if silkStereoUsesAVX2 && len(side) >= len(mid) {
		n = stereoMidSideToLRAVX2(mid, side)
	}
	stereoMidSideToLRScalar(mid[n:], side[n:])
}

//go:noinline
func stereoMidSideToLRAVX2(mid, side []int16) int {
	n := 0
	for ; n+8 <= len(mid); n += 8 {
		mp := (*[8]int16)(unsafe.Pointer(&mid[n]))
		sp := (*[8]int16)(unsafe.Pointer(&side[n]))
		m := archsimd.LoadInt16x8Array(mp)
		s := archsimd.LoadInt16x8Array(sp)
		m.AddSaturated(s).StoreArray(mp)
		m.SubSaturated(s).StoreArray(sp)
	}
	return n
}

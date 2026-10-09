//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// mdctFoldEvenAt deinterleaves the even lanes of one eight-sample block.
func mdctFoldEvenAt(p unsafe.Pointer) archsimd.Float32x4 {
	return loadF32x4(p).ToBits().
		ConcatEven(loadF32x4(unsafe.Add(p, 16)).ToBits()).BitsToFloat32()
}

// mdctFoldEvenDescAt reads base+6, base+4, base+2, base without crossing the
// high end of its backing slice: p points one sample before base.
func mdctFoldEvenDescAt(p unsafe.Pointer) archsimd.Float32x4 {
	lo := loadF32x4(p).ToBits()
	hi := loadF32x4(unsafe.Add(p, 16)).ToBits()
	return reverse4(lo.ConcatOdd(hi).BitsToFloat32())
}

// mdctFoldStore applies the shared twiddle/scale/bit-reversed-scatter tail to a
// computed (re, im) block, matching mdctStoreDirectStageFMALike bit-for-bit:
// yr = (re*t0 - round(im*t1))*preScale, yi = (im*t0 + round(re*t1))*preScale,
// dst[bitrev[j+lane]] = {yr, yi}.
func mdctFoldStore(dst []kissCpx, bitrev []int, t0, t1 unsafe.Pointer, j int, re, im, pv archsimd.Float32x4) {
	tv0 := loadF32x4(t0)
	tv1 := loadF32x4(t1)
	yr := re.MulAdd(tv0, im.Mul(tv1).Neg()).Mul(pv)
	yi := im.MulAdd(tv0, re.Mul(tv1)).Mul(pv)
	// kissCpx is two float32 values. Interleave each result pair and store its
	// packed bits so ARM64 can move each pair with one scalar integer transfer.
	pairLo := yr.ToBits().InterleaveLo(yi.ToBits()).ReshapeToUint64s()
	pairHi := yr.ToBits().InterleaveHi(yi.ToBits()).ReshapeToUint64s()
	*(*uint64)(unsafe.Pointer(&dst[bitrev[j]])) = pairLo.GetElem(0)
	*(*uint64)(unsafe.Pointer(&dst[bitrev[j+1]])) = pairLo.GetElem(1)
	*(*uint64)(unsafe.Pointer(&dst[bitrev[j+2]])) = pairHi.GetElem(0)
	*(*uint64)(unsafe.Pointer(&dst[bitrev[j+3]])) = pairHi.GetElem(1)
}

// mdctFold1StoreNeon is the archsimd leading windowed fold of the forward MDCT.
// Per output j: re = A*wD + round(B*wC), im = A2*wC - round(B2*wD), with
// A=s[xp1+n2+2j], A2=s[xp1+2j], B=s[xp2-2j], B2=s[xp2-n2-2j], wC=w[wp1+2j],
// wD=w[wp2-2j]. Fused MulAdds + single-round Muls match the scalar
// mdctMulAddMixEncode/mdctMulSubMixEncode sequence bit-for-bit.
func mdctFold1StoreNeon(dst []kissCpx, bitrev []int, samples []float32, window []float32, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, preScale float32) {
	if blocks == 0 {
		return
	}
	_ = trig[n4+i0+4*blocks-1]
	_ = bitrev[i0+4*blocks-1]
	pv := archsimd.BroadcastFloat32x4(preScale)
	sp := unsafe.Pointer(unsafe.SliceData(samples))
	wp := unsafe.Pointer(unsafe.SliceData(window))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	// Sample and window cursors point at eight-sample blocks. Descending cursors
	// move back only when another iteration remains, keeping pointers in-bounds.
	a := unsafe.Add(sp, (xp1+n2)*4)
	a2 := unsafe.Add(sp, xp1*4)
	wc := unsafe.Add(wp, wp1*4)
	bp := unsafe.Add(sp, (xp2-7)*4)
	b2 := unsafe.Add(sp, (xp2-n2-7)*4)
	wd := unsafe.Add(wp, (wp2-7)*4)
	t0 := unsafe.Add(tp, i0*4)
	t1 := unsafe.Add(tp, (n4+i0)*4)
	for b := 0; b < blocks; b++ {
		A := mdctFoldEvenAt(a)
		A2 := mdctFoldEvenAt(a2)
		wC := mdctFoldEvenAt(wc)
		B := mdctFoldEvenDescAt(bp)
		B2 := mdctFoldEvenDescAt(b2)
		wD := mdctFoldEvenDescAt(wd)
		re := A.MulAdd(wD, B.Mul(wC))
		im := A2.MulAdd(wC, B2.Mul(wD).Neg())
		mdctFoldStore(dst, bitrev, t0, t1, i0+4*b, re, im, pv)
		if b+1 < blocks {
			a = unsafe.Add(a, 32)
			a2 = unsafe.Add(a2, 32)
			wc = unsafe.Add(wc, 32)
			bp = unsafe.Add(bp, -32)
			b2 = unsafe.Add(b2, -32)
			wd = unsafe.Add(wd, -32)
			t0 = unsafe.Add(t0, 16)
			t1 = unsafe.Add(t1, 16)
		}
	}
}

// mdctFold3StoreNeon is the archsimd trailing windowed fold. Per output j:
// re = fma(-A3, wC, round(B*wD)), im = fma(A2, wD, round(B4*wC)), with
// A3=s[xp1-n2+2j], A2=s[xp1+2j], B=s[xp2-2j], B4=s[xp2+n2-2j], wC=w[wp1+2j],
// wD=w[wp2-2j]. Both fuse the first product as clang -ffp-contract=on does for
// clt_mdct_forward_c(), matching mdctNegMulAddMixEncode/mdctMulAddMixEncode
// bit-for-bit.
func mdctFold3StoreNeon(dst []kissCpx, bitrev []int, samples []float32, window []float32, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, preScale float32) {
	if blocks == 0 {
		return
	}
	_ = trig[n4+i0+4*blocks-1]
	_ = bitrev[i0+4*blocks-1]
	pv := archsimd.BroadcastFloat32x4(preScale)
	sp := unsafe.Pointer(unsafe.SliceData(samples))
	wp := unsafe.Pointer(unsafe.SliceData(window))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	// Each descending cursor starts one sample before its first block so the
	// odd lanes form the desired sequence without reading beyond the high end.
	a3 := unsafe.Add(sp, (xp1-n2)*4)
	a2 := unsafe.Add(sp, xp1*4)
	wc := unsafe.Add(wp, wp1*4)
	bp := unsafe.Add(sp, (xp2-7)*4)
	b4 := unsafe.Add(sp, (xp2+n2-7)*4)
	wd := unsafe.Add(wp, (wp2-7)*4)
	t0 := unsafe.Add(tp, i0*4)
	t1 := unsafe.Add(tp, (n4+i0)*4)
	for b := 0; b < blocks; b++ {
		A3 := mdctFoldEvenAt(a3)
		A2 := mdctFoldEvenAt(a2)
		wC := mdctFoldEvenAt(wc)
		B := mdctFoldEvenDescAt(bp)
		B4 := mdctFoldEvenDescAt(b4)
		wD := mdctFoldEvenDescAt(wd)
		re := A3.Neg().MulAdd(wC, B.Mul(wD))
		im := A2.MulAdd(wD, B4.Mul(wC))
		mdctFoldStore(dst, bitrev, t0, t1, i0+4*b, re, im, pv)
		if b+1 < blocks {
			a3 = unsafe.Add(a3, 32)
			a2 = unsafe.Add(a2, 32)
			wc = unsafe.Add(wc, 32)
			bp = unsafe.Add(bp, -32)
			b4 = unsafe.Add(b4, -32)
			wd = unsafe.Add(wd, -32)
			t0 = unsafe.Add(t0, 16)
			t1 = unsafe.Add(t1, 16)
		}
	}
}

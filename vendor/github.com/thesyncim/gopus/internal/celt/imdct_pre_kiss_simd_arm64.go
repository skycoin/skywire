//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"unsafe"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// imdctPreRotateFMA32Kiss is the archsimd IMDCT pre-rotation. Per output i it
// computes the complex rotation
//
//	x1 = spectrum[2i] (even, ascending); x2 = spectrum[n2-1-2i] (odd, descending)
//	fftIn[i] = (x1*t0 - x2*t1) + i(x2*t0 + x1*t1)
//
// The main loop handles three four-output vector groups at a time. It keeps
// group bases and uses fixed offsets within each group, which reduces loop
// address updates while preserving the rounded product and fused accumulate
// sequence. Remaining vector groups and scalar outputs use the same order.
func imdctPreRotateFMA32Kiss(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	if n4 <= 0 {
		return
	}
	_ = spectrum[n2-1]
	_ = trig[n2-1]
	_ = fftIn[n4-1]
	sp := unsafe.Pointer(unsafe.SliceData(spectrum))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	fp := unsafe.Pointer(unsafe.SliceData(fftIn))
	x1p := sp
	x2p := sp
	if n4 >= 4 {
		// Keep the reverse cursor on the last element in its active range.
		// Each group loads with negative offsets, and the cursor moves only
		// when another vector group remains.
		x2p = unsafe.Add(sp, (n2-1)*4)
	}
	t0p := tp
	t1p := unsafe.Add(tp, n4*4)
	outp := fp
	tailBlocks := n4 % 12 / 4
	for blocks := n4 / 12; blocks > 0; blocks-- {
		x1a := loadF32x4(x1p).ToBits().
			ConcatEven(loadF32x4(unsafe.Add(x1p, 16)).ToBits()).BitsToFloat32()
		x2a := reverse4(loadF32x4(unsafe.Add(x2p, -28)).ToBits().
			ConcatOdd(loadF32x4(unsafe.Add(x2p, -12)).ToBits()).BitsToFloat32())
		t0a := loadF32x4(t0p)
		t1a := loadF32x4(t1p)
		rea := x1a.MulAdd(t0a, x2a.Mul(t1a).Neg())
		ima := x2a.MulAdd(t0a, x1a.Mul(t1a))
		storeF32x4(outp, rea.ToBits().InterleaveLo(ima.ToBits()).BitsToFloat32())
		storeF32x4(unsafe.Add(outp, 16), rea.ToBits().InterleaveHi(ima.ToBits()).BitsToFloat32())

		x1b := loadF32x4(unsafe.Add(x1p, 32)).ToBits().
			ConcatEven(loadF32x4(unsafe.Add(x1p, 48)).ToBits()).BitsToFloat32()
		x2b := reverse4(loadF32x4(unsafe.Add(x2p, -60)).ToBits().
			ConcatOdd(loadF32x4(unsafe.Add(x2p, -44)).ToBits()).BitsToFloat32())
		t0b := loadF32x4(unsafe.Add(t0p, 16))
		t1b := loadF32x4(unsafe.Add(t1p, 16))
		reb := x1b.MulAdd(t0b, x2b.Mul(t1b).Neg())
		imb := x2b.MulAdd(t0b, x1b.Mul(t1b))
		storeF32x4(unsafe.Add(outp, 32), reb.ToBits().InterleaveLo(imb.ToBits()).BitsToFloat32())
		storeF32x4(unsafe.Add(outp, 48), reb.ToBits().InterleaveHi(imb.ToBits()).BitsToFloat32())

		x1c := loadF32x4(unsafe.Add(x1p, 64)).ToBits().
			ConcatEven(loadF32x4(unsafe.Add(x1p, 80)).ToBits()).BitsToFloat32()
		x2c := reverse4(loadF32x4(unsafe.Add(x2p, -92)).ToBits().
			ConcatOdd(loadF32x4(unsafe.Add(x2p, -76)).ToBits()).BitsToFloat32())
		t0c := loadF32x4(unsafe.Add(t0p, 32))
		t1c := loadF32x4(unsafe.Add(t1p, 32))
		rec := x1c.MulAdd(t0c, x2c.Mul(t1c).Neg())
		imc := x2c.MulAdd(t0c, x1c.Mul(t1c))
		storeF32x4(unsafe.Add(outp, 64), rec.ToBits().InterleaveLo(imc.ToBits()).BitsToFloat32())
		storeF32x4(unsafe.Add(outp, 80), rec.ToBits().InterleaveHi(imc.ToBits()).BitsToFloat32())

		if blocks > 1 || tailBlocks > 0 {
			x1p = unsafe.Add(x1p, 96)
			x2p = unsafe.Add(x2p, -96)
			t0p = unsafe.Add(t0p, 48)
			t1p = unsafe.Add(t1p, 48)
			outp = unsafe.Add(outp, 96)
		}
	}
	for blocks := tailBlocks; blocks > 0; blocks-- {
		x1 := loadF32x4(x1p).ToBits().
			ConcatEven(loadF32x4(unsafe.Add(x1p, 16)).ToBits()).BitsToFloat32()
		oddAsc := loadF32x4(unsafe.Add(x2p, -28)).ToBits().
			ConcatOdd(loadF32x4(unsafe.Add(x2p, -12)).ToBits()).BitsToFloat32()
		x2 := reverse4(oddAsc)
		t0 := loadF32x4(t0p)
		t1 := loadF32x4(t1p)
		re := x1.MulAdd(t0, x2.Mul(t1).Neg())
		im := x2.MulAdd(t0, x1.Mul(t1))
		storeF32x4(outp, re.ToBits().InterleaveLo(im.ToBits()).BitsToFloat32())
		storeF32x4(unsafe.Add(outp, 16), re.ToBits().InterleaveHi(im.ToBits()).BitsToFloat32())
		if blocks > 1 {
			x1p = unsafe.Add(x1p, 32)
			x2p = unsafe.Add(x2p, -32)
			t0p = unsafe.Add(t0p, 16)
			t1p = unsafe.Add(t1p, 16)
			outp = unsafe.Add(outp, 32)
		}
	}
	i := n4 - n4%4
	for ; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		fftIn[i] = complex(
			opusmath.FMA32(x1, t0, -noFMA32Mul(x2, t1)),
			opusmath.FMA32(x2, t0, noFMA32Mul(x1, t1)),
		)
	}
}

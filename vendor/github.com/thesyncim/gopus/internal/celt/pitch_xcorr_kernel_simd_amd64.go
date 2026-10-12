//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"math"
	"simd/archsimd"
	"unsafe"

	"github.com/thesyncim/gopus/internal/opusmath"
)

func xcorrKernelAVX8(x, y *float32, sum *[8]float32, length int) {
	if length <= 0 {
		*sum = [8]float32{}
		return
	}
	if !libopusFloatPitchXCorrUsesAVX2FMA() {
		xcorrKernelAVX8ScalarGo(x, y, sum, length)
		return
	}
	tail := newXcorrTail8(unsafe.Pointer(x), length)
	xcorrKernelAVX8Tail(x, y, sum, length, &tail)
}

// xcorrKernelAVX8Tail is celt_pitch_xcorr_avx2's xcorr_kernel_avx for eight
// lags starting at y, with the masked tail block taken from tail.
func xcorrKernelAVX8Tail(x, y *float32, sum *[8]float32, length int, tail *xcorrTail8) {
	if !libopusFloatPitchXCorrUsesAVX2FMA() {
		xcorrKernelAVX8ScalarGo(x, y, sum, length)
		return
	}
	xcorrKernelAVX8OnePassTail(x, y, sum, length, tail)
}

// xcorrTail8 is the masked final block of xcorr_kernel_avx: the rem = length%8
// valid lanes of x, zero elsewhere, and the lane mask _mm256_maskload_ps
// applies to y. One pitch cross-correlation shares it across all lags. When
// yFull is set the caller guarantees eight readable floats at every masked y
// load, so y loads full width and masks with an AND; otherwise it gathers only
// the valid lanes.
type xcorrTail8 struct {
	x     archsimd.Float32x8
	mask  archsimd.Uint32x8
	rem   int
	yFull bool
}

// xcorrTailMaskTable holds eight set lanes followed by eight clear ones;
// xcorrTailMaskTable[8-rem:] starts the mask of the first rem lanes.
var xcorrTailMaskTable = [16]uint32{
	^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0),
}

//go:noinline
func newXcorrTail8(x unsafe.Pointer, length int) xcorrTail8 {
	rem := length & 7
	if rem == 0 {
		return xcorrTail8{}
	}
	return xcorrTail8{
		x:    loadXcorrTail8(unsafe.Add(x, 4*(length-rem)), rem),
		mask: archsimd.LoadUint32x8Array((*[8]uint32)(xcorrTailMaskTable[8-rem:])),
		rem:  rem,
	}
}

// xcorrMaskedY loads the eight floats at p+off and keeps the lanes of mask,
// the _mm256_maskload_ps of a y tail block with eight readable floats.
func xcorrMaskedY(p unsafe.Pointer, off uintptr, mask archsimd.Uint32x8) archsimd.Float32x8 {
	return archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(p, off))).ToBits().And(mask).BitsToFloat32()
}

// xcorrKernelAVX8OnePass keeps all eight correlation accumulators live in one
// loop. Its FMA and lane-reduction order matches xcorrKernelAVX8.
func xcorrKernelAVX8OnePass(x, y *float32, sum *[8]float32, length int) {
	if length <= 0 {
		*sum = [8]float32{}
		return
	}
	if !libopusFloatPitchXCorrUsesAVX2FMA() {
		xcorrKernelAVX8ScalarGo(x, y, sum, length)
		return
	}
	tail := newXcorrTail8(unsafe.Pointer(x), length)
	xcorrKernelAVX8OnePassTail(x, y, sum, length, &tail)
}

//go:noinline
func xcorrKernelAVX8OnePassTail(x, y *float32, sum *[8]float32, length int, tail *xcorrTail8) {
	// Each FMA is written y*x + acc: the product commutes exactly, and the
	// y register is the one the three-operand FMA overwrites, so x stays live
	// without a copy per lag.
	var acc0, acc1, acc2, acc3, acc4, acc5, acc6, acc7 archsimd.Float32x8
	xp, yp := unsafe.Pointer(x), unsafe.Pointer(y)
	i := 0
	for ; i+8 <= length; i += 8 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(xp))
		acc0 = archsimd.LoadFloat32x8Array((*[8]float32)(yp)).MulAdd(xv, acc0)
		acc1 = archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 4))).MulAdd(xv, acc1)
		acc2 = archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 8))).MulAdd(xv, acc2)
		acc3 = archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 12))).MulAdd(xv, acc3)
		// The loop bound implies i < length; this guard keeps the second four
		// y vectors out of the first group's register live range.
		if i < length {
			acc4 = archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 16))).MulAdd(xv, acc4)
			acc5 = archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 20))).MulAdd(xv, acc5)
			acc6 = archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 24))).MulAdd(xv, acc6)
			acc7 = archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 28))).MulAdd(xv, acc7)
		}
		xp = unsafe.Add(xp, 32)
		yp = unsafe.Add(yp, 32)
	}
	if i < length {
		xTail := tail.x
		if tail.yFull {
			m := tail.mask
			acc0 = xcorrMaskedY(yp, 0, m).MulAdd(xTail, acc0)
			acc1 = xcorrMaskedY(yp, 4, m).MulAdd(xTail, acc1)
			acc2 = xcorrMaskedY(yp, 8, m).MulAdd(xTail, acc2)
			acc3 = xcorrMaskedY(yp, 12, m).MulAdd(xTail, acc3)
			acc4 = xcorrMaskedY(yp, 16, m).MulAdd(xTail, acc4)
			acc5 = xcorrMaskedY(yp, 20, m).MulAdd(xTail, acc5)
			acc6 = xcorrMaskedY(yp, 24, m).MulAdd(xTail, acc6)
			acc7 = xcorrMaskedY(yp, 28, m).MulAdd(xTail, acc7)
		} else {
			rem := tail.rem
			acc0 = loadXcorrTail8(yp, rem).MulAdd(xTail, acc0)
			acc1 = loadXcorrTail8(unsafe.Add(yp, 4), rem).MulAdd(xTail, acc1)
			acc2 = loadXcorrTail8(unsafe.Add(yp, 8), rem).MulAdd(xTail, acc2)
			acc3 = loadXcorrTail8(unsafe.Add(yp, 12), rem).MulAdd(xTail, acc3)
			acc4 = loadXcorrTail8(unsafe.Add(yp, 16), rem).MulAdd(xTail, acc4)
			acc5 = loadXcorrTail8(unsafe.Add(yp, 20), rem).MulAdd(xTail, acc5)
			acc6 = loadXcorrTail8(unsafe.Add(yp, 24), rem).MulAdd(xTail, acc6)
			acc7 = loadXcorrTail8(unsafe.Add(yp, 28), rem).MulAdd(xTail, acc7)
		}
	}
	// The eight horizontal sums of xcorr_kernel_avx: [0 4] [1 5] [2 6] [3 7]
	// half sums, then two rounds of pairwise adds leave lag k in lane k.
	s0 := acc0.ConcatPermute128Scalars(0, 2, acc4).Add(acc0.ConcatPermute128Scalars(1, 3, acc4))
	s1 := acc1.ConcatPermute128Scalars(0, 2, acc5).Add(acc1.ConcatPermute128Scalars(1, 3, acc5))
	s2 := acc2.ConcatPermute128Scalars(0, 2, acc6).Add(acc2.ConcatPermute128Scalars(1, 3, acc6))
	s3 := acc3.ConcatPermute128Scalars(0, 2, acc7).Add(acc3.ConcatPermute128Scalars(1, 3, acc7))
	sums := s0.ConcatAddPairsGrouped(s1).ConcatAddPairsGrouped(s2.ConcatAddPairsGrouped(s3))
	sums.StoreArray(sum)
	if sums.IsNaN().ToBits() == 0 {
		return
	}
	// Clear before the legacy-SSE NaN replay; the outer wrapper clears after this block.
	archsimd.ClearAVXUpperBits()
	for corr := range sum {
		if math.Float32bits(sum[corr])&0x7fffffff > 0x7f800000 {
			sum[corr] = opusmath.PitchXcorrAVX2NaNReplay(
				unsafe.Slice(x, length), unsafe.Slice(y, length+7)[corr:], length)
		}
	}
}

func loadXcorrTail8(p unsafe.Pointer, remaining int) archsimd.Float32x8 {
	// Read only valid tail lanes before loading the stack vector, so a tail at a
	// page boundary never turns into a full-width memory read. Integer loads keep
	// the bit patterns exact; the AVX zero store also avoids legacy SSE while
	// correlation accumulators are live.
	var lanes [8]uint32
	var zero archsimd.Uint32x8
	zero.StoreArray(&lanes)
	for lane := range 8 {
		if lane < remaining {
			lanes[lane] = *(*uint32)(unsafe.Add(p, uintptr(lane*4)))
		}
	}
	return archsimd.LoadUint32x8Array(&lanes).BitsToFloat32()
}

func xcorrKernelAVX8ScalarGo(x, y *float32, sum *[8]float32, length int) {
	xs := unsafe.Slice(x, length)
	ys := unsafe.Slice(y, length+7)
	var lanes [8][8]float32
	for i := range xs {
		xv := xs[i]
		for corr := range 8 {
			lane := i & 7
			yv := ys[i+corr]
			if i < 8 {
				// The AVX2 lane starts at +0. Its first fused multiply-add is
				// the product rounded to float32. Keep the FMA for zero or
				// non-finite inputs to preserve its signed-zero and NaN rules.
				product := xv * yv
				if xv != 0 && yv != 0 && product == product {
					lanes[corr][lane] = product
				} else {
					lanes[corr][lane] = opusmath.FMA32(xv, yv, 0)
				}
			} else {
				lanes[corr][lane] = opusmath.FMA32(xv, yv, lanes[corr][lane])
			}
		}
	}
	for corr := range 8 {
		sum[corr] = reduceAVX2PitchSum(lanes[corr])
	}
}

func pitchXcorrKernelAVX8(x, y []float32, sum *[8]float32, length int) {
	xcorrKernelAVX8(&x[0], &y[0], sum, length)
	// Clear the upper register halves the 256-bit kernel leaves dirty, so
	// the caller's scalar SSE code runs without false dependencies.
	archsimd.ClearAVXUpperBits()
}

// pitchXCorrAVX2Blocks runs celt_pitch_xcorr_avx2's eight-lag blocks for
// xcorr[0:maxPitch&^7] and returns the first lag it leaves to the scalar
// tail. The x tail block is prepared once, and a y tail block loads full
// width wherever y's capacity covers it.
func pitchXCorrAVX2Blocks(x, y, xcorr []float32, length, maxPitch int) int {
	if maxPitch < 8 || !libopusFloatPitchXCorrUsesAVX2FMA() {
		return 0
	}
	x = x[:length]
	blocks := maxPitch &^ 7
	_ = xcorr[blocks-1]
	_ = y[blocks+length-2]
	tail := newXcorrTail8(unsafe.Pointer(unsafe.SliceData(x)), length)
	// The widest masked load of lag i+7 reads y[i+7+length-rem : i+length-rem+15].
	fullFrom := cap(y) - (length - tail.rem + 15)
	yp := unsafe.Pointer(unsafe.SliceData(y))
	i := 0
	for ; i < blocks; i += 8 {
		tail.yFull = i <= fullFrom
		sum := (*[8]float32)(xcorr[i : i+8])
		xcorrKernelAVX8Tail(&x[0], (*float32)(unsafe.Add(yp, 4*i)), sum, length, &tail)
	}
	archsimd.ClearAVXUpperBits()
	return i
}

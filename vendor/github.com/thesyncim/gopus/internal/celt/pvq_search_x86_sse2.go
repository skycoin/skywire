//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"unsafe"

	"simd/archsimd"
)

var useX86PVQSearchSSE2 = archsimd.X86.AVX()
var pvqAllOnesI32x4 = [4]int32{-1, -1, -1, -1}
var pvqFourI32x4 = [4]int32{4, 4, 4, 4}

// pvqLoadInt4 and pvqStoreInt4 access four int32 lanes at p[j:j+4]. Callers
// keep j+4 within the buffer's n+3 working length, as libopus's ALLOC(N+3)
// does; loadF32x4/storeF32x4 cover the float32 buffers the same way.
func pvqLoadInt4(p unsafe.Pointer, j int) archsimd.Int32x4 {
	return archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(p, 4*j)))
}

func pvqStoreInt4(p unsafe.Pointer, j int, v archsimd.Int32x4) {
	v.StoreArray((*[4]int32)(unsafe.Add(p, 4*j)))
}

// pvqHorizontalAdd4 is op_pvq_search_sse2's two-shuffle reduction
// sums + shuffle(1,0,3,2), then + shuffle(2,3,0,1), read from lane 0:
// (v0+v2) + (v1+v3).
func pvqHorizontalAdd4(v archsimd.Float32x4) float32 {
	v = v.Add(v.ConcatPermuteScalars(2, 3, 4, 5, v))
	v = v.Add(v.ConcatPermuteScalars(1, 0, 7, 6, v))
	return v.GetElem(0)
}

// pvqBestLane is op_pvq_search_sse2's index reduction: the largest masked
// lane index, max_epi16 over unpackhi_epi64 and then shufflelo_epi16, read
// from lane 0. Indices stay below 2^15, so the 16-bit max equals the 32-bit
// one.
func pvqBestLane(pos archsimd.Int32x4) int32 {
	pos = pos.Max(pos.PermuteScalars(2, 3, 2, 3))
	pos = pos.Max(pos.PermuteScalars(1, 0, 3, 2))
	return pos.GetElem(0)
}

// x86MaxPS4 is _mm_max_ps(a, b): a where a > b, otherwise b, so an unordered
// lane yields b. archsimd's Max is commutative to the compiler, which may swap
// its operands and change the unordered result.
func x86MaxPS4(a, b archsimd.Float32x4) archsimd.Float32x4 {
	return a.IfElse(a.Greater(b), b)
}

// x86PVQSearchBestIDSSE2 is one pulse iteration of op_pvq_search_sse2's
// search: four lanes keep their running max (max_ps) and the index of its
// first strict improvement, then the lane maxima reduce through the same
// shuffles, and the largest index among lanes equal to the reduced max wins.
// It reproduces _mm_max_ps for any input, NaN included.
func x86PVQSearchBestIDSSE2(absX, y []float32, xy, yy float32, n int) int {
	return x86PVQSearchBestIDExact(unsafe.Pointer(unsafe.SliceData(absX)), unsafe.Pointer(unsafe.SliceData(y)), xy, yy, n)
}

func x86PVQSearchBestIDExact(xp, yp unsafe.Pointer, xy, yy float32, n int) int {
	xy4 := broadcastF32x4Arch(xy)
	yy4 := broadcastF32x4Arch(yy)
	laneMax := archsimd.Float32x4{}
	pos := archsimd.Int32x4{}
	count := archsimd.LoadInt32x4Array(&[4]int32{0, 1, 2, 3})
	four := archsimd.LoadInt32x4Array(&pvqFourI32x4)
	for j := 0; j < n; j += 4 {
		x4 := loadF32x4(unsafe.Add(xp, 4*j)).Add(xy4)
		y4 := loadF32x4(unsafe.Add(yp, 4*j)).Add(yy4).ReciprocalSqrt()
		r4 := x4.Mul(y4)
		pos = pos.Max(count.And(r4.Greater(laneMax).ToInt32x4()))
		laneMax = x86MaxPS4(laneMax, r4)
		count = count.Add(four)
	}
	max2 := x86MaxPS4(laneMax, laneMax.ConcatPermuteScalars(2, 3, 4, 5, laneMax))
	max2 = x86MaxPS4(max2, max2.ConcatPermuteScalars(1, 0, 7, 6, max2))
	return int(pvqBestLane(pos.And(laneMax.Equal(max2).ToInt32x4())))
}

// x86PVQPulsesFinite runs op_pvq_search_sse2's pulse loop for scores that
// are never NaN, placing pulses one at a time exactly as
// x86PVQSearchBestIDExact would choose them. max_ps then only differs from a
// commuted max in the sign of an equal zero, which no later comparison
// observes. It returns the updated xy and yy.
func x86PVQPulsesFinite(xp, yp, iyp unsafe.Pointer, xy, yy float32, n, pulses int) (float32, float32) {
	ids := archsimd.LoadInt32x4Array(&[4]int32{0, 1, 2, 3})
	four := archsimd.LoadInt32x4Array(&pvqFourI32x4)
	for range pulses {
		yy++
		xy4 := broadcastF32x4Arch(xy)
		yy4 := broadcastF32x4Arch(yy)
		laneMax := archsimd.Float32x4{}
		pos := archsimd.Int32x4{}
		count := ids
		for j := 0; j < n; j += 4 {
			x4 := loadF32x4(unsafe.Add(xp, 4*j)).Add(xy4)
			y4 := loadF32x4(unsafe.Add(yp, 4*j)).Add(yy4).ReciprocalSqrt()
			r4 := x4.Mul(y4)
			pos = pos.Max(count.And(r4.Greater(laneMax).ToInt32x4()))
			laneMax = laneMax.Max(r4)
			count = count.Add(four)
		}
		max2 := laneMax.Max(laneMax.ConcatPermuteScalars(2, 3, 4, 5, laneMax))
		max2 = max2.Max(max2.ConcatPermuteScalars(1, 0, 7, 6, max2))
		best := uintptr(pvqBestLane(pos.And(laneMax.Equal(max2).ToInt32x4())))
		xy += *(*float32)(unsafe.Add(xp, 4*best))
		yb := (*float32)(unsafe.Add(yp, 4*best))
		yy += *yb
		*yb += 2
		*(*int32)(unsafe.Add(iyp, 4*best))++
	}
	return xy, yy
}

// pvqFiniteBound bounds |x| so that no pulse search score can become NaN:
// xy stays below K*2^60 and every score is a finite product.
const pvqFiniteBound = float32(1 << 60)

// opPVQSearchScratchNormX86SSE2 mirrors libopus 1.6.1
// celt/x86/vq_sse2.c:op_pvq_search_sse2 lane for lane: the absolute-value and
// sum pass, the pyramid projection, and the pulse search run on four float32
// lanes with the same rcp/rsqrt approximation points and the same shuffle
// reductions. The caller's x is left untouched; the search works on a copy.
func opPVQSearchScratchNormX86SSE2(x []celtNorm, k int, iyBuf *[]int32, signxBuf *[]byte, yBuf *[]float32, absXBuf *[]float32, absInput bool) ([]int32, opusVal16) {
	_, _ = signxBuf, absInput
	n := len(x)
	workN := n + 3
	var iy []int32
	if iyBuf != nil {
		iy = ensureInt32Slice(iyBuf, workN)
	} else {
		iy = make([]int32, workN)
	}
	if n == 0 || k <= 0 {
		clear(iy)
		return iy[:n], 0
	}

	var y, absX []float32
	if yBuf != nil {
		y = ensureFloat32Slice(yBuf, workN)
	} else {
		y = make([]float32, workN)
	}
	if absXBuf != nil {
		absX = ensureFloat32Slice(absXBuf, workN)
	} else {
		absX = make([]float32, workN)
	}
	xp := unsafe.Pointer(unsafe.SliceData(absX))
	// The absolute-value pass reads x in whole vectors. A length that is not
	// a multiple of four reads the zero padding of a copy, as libopus reads
	// X[N..N+2] = 0 of its own.
	src := unsafe.Pointer(unsafe.SliceData(x))
	if n&3 != 0 {
		copy(absX, x)
		absX[n], absX[n+1], absX[n+2] = 0, 0, 0
		src = xp
	}
	yp := unsafe.Pointer(unsafe.SliceData(y))
	iyp := unsafe.Pointer(unsafe.SliceData(iy))
	zero := archsimd.Float32x4{}
	zeroInt := archsimd.Int32x4{}

	sums := zero
	bound := broadcastF32x4Arch(pvqFiniteBound)
	finite := archsimd.LoadInt32x4Array(&pvqAllOnesI32x4)
	for j := 0; j < n; j += 4 {
		x4 := absF32x4AVX(loadF32x4(unsafe.Add(src, 4*j)))
		finite = finite.And(x4.Less(bound).ToInt32x4())
		sums = sums.Add(x4)
		storeF32x4(unsafe.Add(yp, 4*j), zero)
		pvqStoreInt4(iyp, j, zeroInt)
		storeF32x4(unsafe.Add(xp, 4*j), x4)
	}
	sum := pvqHorizontalAdd4(sums)

	var xy, yy float32
	pulsesLeft := k
	if k > n>>1 {
		if !(sum > pvqEPSILON && sum < 64) {
			absX[0] = 1
			clear(absX[1:n])
			sum = 1
		}
		// (float)(K+.8) times _mm_rcp_ps(sums); every lane of sums holds sum.
		rcp4 := broadcastF32x4Arch(float32(k) + 0.8).Mul(broadcastF32x4Arch(sum).Reciprocal())
		xy4, yy4 := zero, zero
		pulses := zeroInt
		for j := 0; j < n; j += 4 {
			x4 := loadF32x4(unsafe.Add(xp, 4*j))
			iy4 := x4.Mul(rcp4).ConvertToInt32()
			pulses = pulses.Add(iy4)
			pvqStoreInt4(iyp, j, iy4)
			y4 := iy4.ConvertToFloat32()
			xy4 = xy4.Add(x4.Mul(y4))
			yy4 = yy4.Add(y4.Mul(y4))
			storeF32x4(unsafe.Add(yp, 4*j), y4.Add(y4))
		}
		pulsesLeft -= int((pulses.GetElem(0) + pulses.GetElem(2)) + (pulses.GetElem(1) + pulses.GetElem(3)))
		xy = pvqHorizontalAdd4(xy4)
		yy = pvqHorizontalAdd4(yy4)
	}
	absX[n], absX[n+1], absX[n+2] = -100, -100, -100
	y[n], y[n+1], y[n+2] = 100, 100, 100

	if pulsesLeft > n+3 {
		tmp := float32(pulsesLeft)
		yy = add32(yy, mul32(tmp, tmp))
		yy = add32(yy, mul32(tmp, y[0]))
		iy[0] += int32(pulsesLeft)
		pulsesLeft = 0
	}

	if finite.GetElem(0)&finite.GetElem(1)&finite.GetElem(2)&finite.GetElem(3) != 0 {
		xy, yy = x86PVQPulsesFinite(xp, yp, iyp, xy, yy, n, pulsesLeft)
	} else {
		for range pulsesLeft {
			yy++
			best := x86PVQSearchBestIDExact(xp, yp, xy, yy, n)
			xy += absX[best]
			yy += y[best]
			y[best] += 2
			iy[best]++
		}
	}

	// Restore the signs of x: iy = (iy ^ s) - s with s = -(x < 0).
	j := 0
	for ; j+4 <= n; j += 4 {
		s := archsimd.LoadFloat32x4Array((*[4]float32)(x[j : j+4])).Less(zero).ToInt32x4()
		iy4 := pvqLoadInt4(iyp, j)
		pvqStoreInt4(iyp, j, iy4.Xor(s).Sub(s))
	}
	for ; j < n; j++ {
		if x[j] < 0 {
			iy[j] = -iy[j]
		}
	}
	return iy[:n], opusVal16(yy)
}

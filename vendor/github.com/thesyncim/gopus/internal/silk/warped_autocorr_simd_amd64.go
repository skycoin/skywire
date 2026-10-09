//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

var silkWarpedAutocorrUsesAVX2 = archsimd.X86.AVX2()

// warpedAutocorrelationSections runs silk_warped_autocorrelation_FLP eight
// input samples at a time as a wavefront: lane j of the Float64x4 pair (lanes
// 0-3 in the first vector, 4-7 in the second) carries sample n+j through
// allpass pair p = q-j at step q. A pair of sample n+j needs only sample
// n+j-1's results for the same pair and the pair after it, which lane j-1
// produced one step earlier, so the eight serial chains overlap while every
// state and correlation value sees the same double operations in the same
// order as the one-sample loop. The correlation accumulators travel with the
// lanes too, so each C[i] still sums the samples in input order.
func warpedAutocorrelationSections(st, corr *warpedAutocorrState, in []float32, w silkCReal, order int) {
	n := 0
	if silkWarpedAutocorrUsesAVX2 {
		n = warpedAutocorrelationSectionsAVX2(st, corr, in, w, order)
	}
	warpedAutocorrelationSamples(st, corr, in[n:], w, order)
}

// warpedAutocorrelationSectionsAVX2 runs the whole groups of eight samples
// and returns how many samples it consumed. Between its first 256-bit
// instruction and the closing ClearAVXUpperBits it runs no legacy SSE
// instruction: cores that save and restore the upper register halves around
// such an instruction charge a state transition for each one, so the scalar
// tail and the C double warping argument stay outside the vector region.
//
//go:noinline
func warpedAutocorrelationSectionsAVX2(st, corr *warpedAutocorrState, in []float32, w silkCReal, order int) int {
	pairs := order / 2
	warp := archsimd.BroadcastFloat64x4(w)
	// startMask[k] selects the lanes at and above k: at step q < 8 lane q
	// starts its sample, and the lanes above it have not started yet.
	var startMask [4]archsimd.Mask64x4
	lanes := archsimd.LoadInt64x4Array(&[4]int64{0, 1, 2, 3})
	for k := range startMask {
		startMask[k] = lanes.Greater(archsimd.BroadcastInt64x4(int64(k) - 1))
	}
	// Row i of a state array starts at the element before C double i, so
	// its second element is C double i.
	st1 := unsafe.Pointer(&st[0])
	corr1 := unsafe.Pointer(&corr[0])
	n := 0
	for ; n+8 <= len(in); n += 8 {
		// The inputs are also the state[0] factors of each lane's
		// correlation terms.
		xLo := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Pointer(&in[n]))).ConvertToFloat64()
		xHi := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Pointer(&in[n+4]))).ConvertToFloat64()
		// t1 is each lane's tmp1 entering its pair; prevT1 and t2 are the
		// previous step's tmp1 input and tmp2 result.
		var t1Lo, prevT1Lo, t2Lo, ceLo, coLo archsimd.Float64x4
		var t1Hi, prevT1Hi, t2Hi, ceHi, coHi archsimd.Float64x4
		for q := 0; q <= pairs+7; q++ {
			if q < 4 {
				t1Lo = xLo.IfElse(startMask[q], t1Lo)
			} else if q < 8 {
				t1Hi = xHi.IfElse(startMask[q-4], t1Hi)
			}
			// Lane 0 reads the state and correlations sample n-1 left in
			// memory; lane j>0 takes the values lane j-1 produced for its
			// sample.
			aLo := warpedShiftIn(prevT1Lo, st1, 2*q)
			bLo := warpedShiftIn(t2Lo, st1, 2*q+1)
			cLo := warpedShiftIn(t1Lo, st1, 2*q+2)
			aHi := warpedShiftAcross(prevT1Lo, prevT1Hi)
			bHi := warpedShiftAcross(t2Lo, t2Hi)
			cHi := warpedShiftAcross(t1Lo, t1Hi)
			// tmp2 = state[i] + warping*state[i+1] - warping*tmp1
			t2nLo := aLo.Add(warp.Mul(bLo)).Sub(warp.Mul(t1Lo))
			t2nHi := aHi.Add(warp.Mul(bHi)).Sub(warp.Mul(t1Hi))
			// tmp1 = state[i+1] + warping*state[i+2] - warping*tmp2
			t1nLo := bLo.Add(warp.Mul(cLo)).Sub(warp.Mul(t2nLo))
			t1nHi := bHi.Add(warp.Mul(cHi)).Sub(warp.Mul(t2nHi))
			// C[i] += state[0]*tmp1; C[i+1] += state[0]*tmp2
			ceHi = warpedShiftAcross(ceLo, ceHi).Add(xHi.Mul(t1Hi))
			coHi = warpedShiftAcross(coLo, coHi).Add(xHi.Mul(t2nHi))
			ceLo = warpedShiftIn(ceLo, corr1, 2*q).Add(xLo.Mul(t1Lo))
			coLo = warpedShiftIn(coLo, corr1, 2*q+1).Add(xLo.Mul(t2nLo))

			// Lane 7 holds the last sample of the group: its pair p = q-7
			// leaves the final state and correlations for the next group.
			// At p == pairs only the first double of each pair is C double
			// order; the second is padding that only discarded lanes read.
			if p := q - 7; p >= 0 && p <= pairs {
				warpedLane3Pair(t1Hi, t2nHi).StoreArray((*[2]silkCReal)(st[1+2*p : 3+2*p]))
				warpedLane3Pair(ceHi, coHi).StoreArray((*[2]silkCReal)(corr[1+2*p : 3+2*p]))
			}
			prevT1Lo, t1Lo, t2Lo = t1Lo, t1nLo, t2nLo
			prevT1Hi, t1Hi, t2Hi = t1Hi, t1nHi, t2nHi
		}
	}
	// The wavefront leaves the upper register halves dirty; clear them
	// before the scalar SSE loop.
	archsimd.ClearAVXUpperBits()
	return n
}

// warpedShiftIn returns {c[i], x[0], x[1], x[2]}: every lane takes the value
// of the lane below it, and lane 0 takes C double i of the warpedAutocorrState
// that starts at v.
func warpedShiftIn(x archsimd.Float64x4, v unsafe.Pointer, i int) archsimd.Float64x4 {
	row := archsimd.LoadFloat64x4Array((*[4]silkCReal)(unsafe.Add(v, 8*i))) // {c[i-1], c[i], ...}
	u := row.ConcatPermute128Scalars(0, 2, x)                               // {c[i-1], c[i], x0, x1}
	return u.ConcatPermuteScalarsGrouped(1, 2, x)                           // {c[i], x0, x1, x2}
}

// warpedShiftAcross returns {lo[3], hi[0], hi[1], hi[2]}: lane 4 of the
// eight-lane wavefront takes the value of lane 3.
func warpedShiftAcross(lo, hi archsimd.Float64x4) archsimd.Float64x4 {
	u := lo.ConcatPermute128Scalars(1, 2, hi) // {lo2, lo3, hi0, hi1}
	return u.ConcatPermuteScalarsGrouped(1, 2, hi)
}

// warpedLane3Pair returns {a[3], b[3]}.
func warpedLane3Pair(a, b archsimd.Float64x4) archsimd.Float64x2 {
	return a.ConcatPermuteScalarsGrouped(1, 3, b).GetHi()
}

//go:build amd64 && goexperiment.simd && !nosimd && !purego

package opusmath

import (
	"math"
	"simd/archsimd"
)

const x86InvalidNaN32 = uint32(0xffc00000)

func x86QuietNaN32(bits uint32) uint32 { return bits | 0x00400000 }

func x86IsNaN32(bits uint32) bool {
	return bits&0x7f800000 == 0x7f800000 && bits&0x007fffff != 0
}

// x86PitchFMA231 follows the NaN operand order of the VFMADD231PS instructions
// emitted for libopus celt/x86/pitch_avx.c:xcorr_kernel_avx. This is reached
// only when the vector result is NaN; finite correlations stay on the SIMD path.
func x86PitchFMA231(x, y, acc float32) float32 {
	if bits := math.Float32bits(x); x86IsNaN32(bits) {
		return math.Float32frombits(x86QuietNaN32(bits))
	}
	if bits := math.Float32bits(y); x86IsNaN32(bits) {
		return math.Float32frombits(x86QuietNaN32(bits))
	}
	if bits := math.Float32bits(acc); x86IsNaN32(bits) {
		return math.Float32frombits(x86QuietNaN32(bits))
	}
	// A 128-bit VFMADD231PS lane rounds as the 256-bit one does and leaves the
	// upper register halves clean for the legacy-SSE replay around it.
	result := archsimd.BroadcastFloat32x4(x).MulAdd(
		archsimd.BroadcastFloat32x4(y), archsimd.BroadcastFloat32x4(acc)).GetElem(0)
	if result != result {
		return math.Float32frombits(x86InvalidNaN32)
	}
	return result
}

func x86PitchMul(first, second float32) float32 {
	if bits := math.Float32bits(first); x86IsNaN32(bits) {
		return math.Float32frombits(x86QuietNaN32(bits))
	}
	if bits := math.Float32bits(second); x86IsNaN32(bits) {
		return math.Float32frombits(x86QuietNaN32(bits))
	}
	result := first * second
	if result != result {
		return math.Float32frombits(x86InvalidNaN32)
	}
	return result
}

func x86PitchAdd(first, second float32) float32 {
	if bits := math.Float32bits(first); x86IsNaN32(bits) {
		return math.Float32frombits(x86QuietNaN32(bits))
	}
	if bits := math.Float32bits(second); x86IsNaN32(bits) {
		return math.Float32frombits(x86QuietNaN32(bits))
	}
	result := first + second
	if result != result {
		return math.Float32frombits(x86InvalidNaN32)
	}
	return result
}

// PitchXcorrAVX2NaNReplay reproduces the selected AVX2 correlation's NaN bits
// from libopus celt/x86/pitch_avx.c:xcorr_kernel_avx. The compiled kernel uses
// the high half as the first VADDPS operand, then two VHADDPS pair stages.
// The replay has fixed stack scratch and is called only for a NaN SIMD result.
func PitchXcorrAVX2NaNReplay(x, y []float32, length int) float32 {
	var lanes [8]float32
	for i := 0; i < length; i++ {
		lane := i & 7
		lanes[lane] = x86PitchFMA231(x[i], y[i], lanes[lane])
	}
	if remaining := length & 7; remaining != 0 {
		for lane := remaining; lane < 8; lane++ {
			lanes[lane] = x86PitchFMA231(0, 0, lanes[lane])
		}
	}
	a := x86PitchAdd(lanes[4], lanes[0])
	b := x86PitchAdd(lanes[5], lanes[1])
	c := x86PitchAdd(lanes[6], lanes[2])
	d := x86PitchAdd(lanes[7], lanes[3])
	return x86PitchAdd(x86PitchAdd(a, b), x86PitchAdd(c, d))
}

// PitchXcorrSSENaNReplay follows celt/x86/pitch_sse.c:celt_inner_prod_sse.
// MULPS and ADDPS keep their destination as the first NaN operand. The high
// lane pair is the first operand in the horizontal sum. The non-v3 split tail
// multiplies y*x for its third sample in the linked libopus 1.6.1 output; the
// v3 tail uses a fused multiply-add with x and y in source order.
func PitchXcorrSSENaNReplay(x, y []float32, length int) float32 {
	var lanes [4]float32
	i := 0
	for ; i+4 <= length; i += 4 {
		for lane := 0; lane < 4; lane++ {
			lanes[lane] = x86PitchAdd(lanes[lane], x86PitchMul(x[i+lane], y[i+lane]))
		}
	}
	sum := x86PitchAdd(x86PitchAdd(lanes[2], lanes[0]), x86PitchAdd(lanes[3], lanes[1]))
	for tail := 0; i < length; i, tail = i+1, tail+1 {
		if x86PitchSSETailUsesFMA {
			sum = x86PitchFMA231(x[i], y[i], sum)
		} else if tail == 2 {
			sum = x86PitchAdd(sum, x86PitchMul(y[i], x[i]))
		} else {
			sum = x86PitchAdd(sum, x86PitchMul(x[i], y[i]))
		}
	}
	return sum
}

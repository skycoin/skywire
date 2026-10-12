//go:build arm64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

const usePitchXcorrArm64SIMD = true

func celtPitchXcorrFloatImplArm64SIMD(x, y []float32, out []float32, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	vectorPitchCount := maxPitch &^ 3
	_ = x[length-1]
	_ = y[length+maxPitch-2]
	_ = out[maxPitch-1]
	// These checks establish the input extents used by the four-lane pointer loads.
	xBase := unsafe.Pointer(unsafe.SliceData(x))
	yBase := unsafe.Pointer(unsafe.SliceData(y))

	lag := 0
	for ; lag+8 <= vectorPitchCount; lag += 8 {
		pitchXcorrAccumulateEightLags(xBase, yBase, out, lag, length)
	}
	for ; lag < vectorPitchCount; lag += 4 {
		acc := archsimd.BroadcastFloat32x4(0)
		i := 0
		for ; i+8 < length; i += 8 {
			y0 := pitchXcorrLoadFloat32x4At(yBase, lag+i)
			y1 := pitchXcorrLoadFloat32x4At(yBase, lag+i+4)
			y2 := pitchXcorrLoadFloat32x4At(yBase, lag+i+8)
			x0 := pitchXcorrLoadFloat32x4At(xBase, i)
			x1 := pitchXcorrLoadFloat32x4At(xBase, i+4)
			acc = y0.MulAdd(archsimd.BroadcastFloat32x4(x0.GetElem(0)), acc)
			acc = pitchXcorrShift4(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x0.GetElem(1)), acc)
			acc = pitchXcorrShift8(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x0.GetElem(2)), acc)
			acc = pitchXcorrShift12(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x0.GetElem(3)), acc)
			acc = y1.MulAdd(archsimd.BroadcastFloat32x4(x1.GetElem(0)), acc)
			acc = pitchXcorrShift4(y1, y2).MulAdd(archsimd.BroadcastFloat32x4(x1.GetElem(1)), acc)
			acc = pitchXcorrShift8(y1, y2).MulAdd(archsimd.BroadcastFloat32x4(x1.GetElem(2)), acc)
			acc = pitchXcorrShift12(y1, y2).MulAdd(archsimd.BroadcastFloat32x4(x1.GetElem(3)), acc)
		}
		if i+4 < length {
			y0 := pitchXcorrLoadFloat32x4At(yBase, lag+i)
			y1 := pitchXcorrLoadFloat32x4At(yBase, lag+i+4)
			acc = y0.MulAdd(archsimd.BroadcastFloat32x4(pitchXcorrLoadFloat32At(xBase, i)), acc)
			acc = pitchXcorrShift4(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(pitchXcorrLoadFloat32At(xBase, i+1)), acc)
			acc = pitchXcorrShift8(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(pitchXcorrLoadFloat32At(xBase, i+2)), acc)
			acc = pitchXcorrShift12(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(pitchXcorrLoadFloat32At(xBase, i+3)), acc)
			i += 4
		}
		for ; i < length; i++ {
			acc = pitchXcorrLoadFloat32x4At(yBase, lag+i).MulAdd(archsimd.BroadcastFloat32x4(pitchXcorrLoadFloat32At(xBase, i)), acc)
		}
		acc.Store(out[lag : lag+4])
	}
	for lag := vectorPitchCount; lag < maxPitch; lag++ {
		out[lag] = pitchXcorrInnerProductArm64SIMD(x, y[lag:], length)
	}
}

// pitchXcorrAccumulateEightLags interleaves two ordered four-lag FMA chains.
func pitchXcorrAccumulateEightLags(xBase, yBase unsafe.Pointer, out []float32, lag, length int) {
	acc0 := archsimd.BroadcastFloat32x4(0)
	acc1 := archsimd.BroadcastFloat32x4(0)
	i := 0
	for ; i+8 < length; i += 8 {
		y0 := pitchXcorrLoadFloat32x4At(yBase, lag+i)
		y1 := pitchXcorrLoadFloat32x4At(yBase, lag+i+4)
		y2 := pitchXcorrLoadFloat32x4At(yBase, lag+i+8)
		y3 := pitchXcorrLoadFloat32x4At(yBase, lag+i+12)
		x0 := pitchXcorrLoadFloat32x4At(xBase, i)
		x1 := pitchXcorrLoadFloat32x4At(xBase, i+4)
		value := archsimd.BroadcastFloat32x4(x0.GetElem(0))
		acc0 = y0.MulAdd(value, acc0)
		acc1 = y1.MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x0.GetElem(1))
		acc0 = pitchXcorrShift4(y0, y1).MulAdd(value, acc0)
		acc1 = pitchXcorrShift4(y1, y2).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x0.GetElem(2))
		acc0 = pitchXcorrShift8(y0, y1).MulAdd(value, acc0)
		acc1 = pitchXcorrShift8(y1, y2).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x0.GetElem(3))
		acc0 = pitchXcorrShift12(y0, y1).MulAdd(value, acc0)
		acc1 = pitchXcorrShift12(y1, y2).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x1.GetElem(0))
		acc0 = y1.MulAdd(value, acc0)
		acc1 = y2.MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x1.GetElem(1))
		acc0 = pitchXcorrShift4(y1, y2).MulAdd(value, acc0)
		acc1 = pitchXcorrShift4(y2, y3).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x1.GetElem(2))
		acc0 = pitchXcorrShift8(y1, y2).MulAdd(value, acc0)
		acc1 = pitchXcorrShift8(y2, y3).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x1.GetElem(3))
		acc0 = pitchXcorrShift12(y1, y2).MulAdd(value, acc0)
		acc1 = pitchXcorrShift12(y2, y3).MulAdd(value, acc1)
	}
	if i+4 < length {
		y0 := pitchXcorrLoadFloat32x4At(yBase, lag+i)
		y1 := pitchXcorrLoadFloat32x4At(yBase, lag+i+4)
		y2 := pitchXcorrLoadFloat32x4At(yBase, lag+i+8)
		x0 := pitchXcorrLoadFloat32x4At(xBase, i)
		value := archsimd.BroadcastFloat32x4(x0.GetElem(0))
		acc0 = y0.MulAdd(value, acc0)
		acc1 = y1.MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x0.GetElem(1))
		acc0 = pitchXcorrShift4(y0, y1).MulAdd(value, acc0)
		acc1 = pitchXcorrShift4(y1, y2).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x0.GetElem(2))
		acc0 = pitchXcorrShift8(y0, y1).MulAdd(value, acc0)
		acc1 = pitchXcorrShift8(y1, y2).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x0.GetElem(3))
		acc0 = pitchXcorrShift12(y0, y1).MulAdd(value, acc0)
		acc1 = pitchXcorrShift12(y1, y2).MulAdd(value, acc1)
		i += 4
	}
	for ; i < length; i++ {
		value := archsimd.BroadcastFloat32x4(pitchXcorrLoadFloat32At(xBase, i))
		acc0 = pitchXcorrLoadFloat32x4At(yBase, lag+i).MulAdd(value, acc0)
		acc1 = pitchXcorrLoadFloat32x4At(yBase, lag+4+i).MulAdd(value, acc1)
	}
	acc0.Store(out[lag : lag+4])
	acc1.Store(out[lag+4 : lag+8])
}

// These loaders rely on the caller's extent checks and loop bounds.
func pitchXcorrLoadFloat32At(base unsafe.Pointer, index int) float32 {
	return *(*float32)(unsafe.Add(base, uintptr(index)*unsafe.Sizeof(float32(0))))
}

func pitchXcorrLoadFloat32x4At(base unsafe.Pointer, index int) archsimd.Float32x4 {
	ptr := (*float32)(unsafe.Add(base, uintptr(index)*unsafe.Sizeof(float32(0))))
	return archsimd.LoadFloat32x4((*[4]float32)(unsafe.Pointer(ptr))[:])
}

func pitchXcorrInnerProductArm64SIMD(x, y []float32, length int) float32 {
	acc := archsimd.BroadcastFloat32x4(0)
	i := 0
	// celt_inner_prod_neon calls vmlaq_f32(acc, x, y) at
	// pitch_neon_intr.c:196. Reversing Go MulAdd operands emits the same
	// vector FMLA source order and NaN payload precedence.
	for ; i+8 <= length; i += 8 {
		acc = archsimd.LoadFloat32x4(y[i:]).MulAdd(archsimd.LoadFloat32x4(x[i:]), acc)
		acc = archsimd.LoadFloat32x4(y[i+4:]).MulAdd(archsimd.LoadFloat32x4(x[i+4:]), acc)
	}
	if i+4 <= length {
		acc = archsimd.LoadFloat32x4(y[i:]).MulAdd(archsimd.LoadFloat32x4(x[i:]), acc)
		i += 4
	}
	// celt_inner_prod_neon reduces low+high pairs with vadd_f32 and then
	// vpadd_f32. Reversing the Go expression order matches the instruction
	// operand order and keeps the same NaN payload selection.
	sum := (acc.GetElem(2) + acc.GetElem(0)) + (acc.GetElem(3) + acc.GetElem(1))
	for ; i < length; i++ {
		// celt_inner_prod_neon finishes its scalar remainder with
		// MAC16_16(sum, x[i], y[i]) at pitch_neon_intr.c:214. This operand
		// order makes Go lower to the same arm64 FMADD source order, including
		// which NaN payload the scalar tail returns.
		sum = y[i]*x[i] + sum
	}
	return sum
}

func pitchXcorrShift4(lo, hi archsimd.Float32x4) archsimd.Float32x4 {
	return hi.ToBits().ReshapeToUint8s().ConcatShiftBytesRight(lo.ToBits().ReshapeToUint8s(), 4).ReshapeToUint32s().BitsToFloat32()
}

func pitchXcorrShift8(lo, hi archsimd.Float32x4) archsimd.Float32x4 {
	return hi.ToBits().ReshapeToUint8s().ConcatShiftBytesRight(lo.ToBits().ReshapeToUint8s(), 8).ReshapeToUint32s().BitsToFloat32()
}

func pitchXcorrShift12(lo, hi archsimd.Float32x4) archsimd.Float32x4 {
	return hi.ToBits().ReshapeToUint8s().ConcatShiftBytesRight(lo.ToBits().ReshapeToUint8s(), 12).ReshapeToUint32s().BitsToFloat32()
}

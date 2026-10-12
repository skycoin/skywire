//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// reverse4 reverses the four float32 lanes with operations available in Go
// 1.27's arm64 archsimd API. Pair swap plus a 64-bit rotate produces
// [d,c,b,a] without extracting lanes through scalar registers.
func reverse4(v archsimd.Float32x4) archsimd.Float32x4 {
	bits := v.ToBits()
	pairSwapped := bits.ConcatOdd(bits).InterleaveLo(bits.ConcatEven(bits))
	bytes := pairSwapped.ReshapeToUint8s()
	return bytes.ConcatShiftBytesRight(bytes, 8).ReshapeToUint32s().BitsToFloat32()
}

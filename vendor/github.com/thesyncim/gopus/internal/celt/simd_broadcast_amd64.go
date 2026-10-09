//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// broadcastF32x4Arch creates a repeated lane with AVX1 instructions. The
// archsimd.BroadcastFloat32x4 lowering uses VBROADCASTSS XMM,XMM, an AVX2-only
// encoding in the Go 1.27 amd64 backend.
func broadcastF32x4Arch(value float32) archsimd.Float32x4 {
	var zero archsimd.Float32x4
	first := zero.SetElem(0, value)
	return first.ConcatPermuteScalars(0, 0, 0, 0, first)
}

func hasCELTFloat32x4SIMD() bool { return archsimd.X86.AVX() }

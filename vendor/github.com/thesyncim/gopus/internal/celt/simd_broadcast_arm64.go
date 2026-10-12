//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

func broadcastF32x4Arch(value float32) archsimd.Float32x4 {
	return archsimd.BroadcastFloat32x4(value)
}

func hasCELTFloat32x4SIMD() bool { return true }

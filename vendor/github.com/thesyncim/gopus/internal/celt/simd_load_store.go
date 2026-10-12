//go:build (amd64 || arm64) && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// loadF32x4 / storeF32x4 read and write a four-lane vector through a raw pointer,
// avoiding the slice length checks emitted by archsimd.LoadFloat32x4(s[i:]) and
// .Store(s[i:]) on each call. Both helpers inline to a vector load/store; callers
// guarantee that the pointer addresses four in-range float32 values.
func loadF32x4(p unsafe.Pointer) archsimd.Float32x4 {
	return archsimd.LoadFloat32x4Array((*[4]float32)(p))
}

func storeF32x4(p unsafe.Pointer, v archsimd.Float32x4) {
	v.StoreArray((*[4]float32)(p))
}

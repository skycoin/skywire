//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// celtPLCXcorrKernel4Float32SSE matches celt/celt_lpc.c:celt_fir_c calling
// celt/x86/pitch_sse.c:xcorr_kernel_sse in the pinned x86-v3 SIMD build. GCC
// contracts each packed multiply/add into FMA, and keeps the same even/odd
// accumulator order as the existing scalar-shaped SSE-order helper.
func celtPLCXcorrKernel4Float32SSE(x, y []float32, sum *[4]float32, length int) {
	if length <= 0 {
		return
	}
	x = x[:length]
	y = y[:length+3]
	yp := unsafe.Pointer(unsafe.SliceData(y))
	accEven := loadF32x4(unsafe.Pointer(&sum[0]))
	var accOdd archsimd.Float32x4

	j := 0
	for ; j+4 <= length; j += 4 {
		yj := unsafe.Add(yp, uintptr(j)*4)
		accEven = broadcastF32x4Arch(x[j]).MulAdd(loadF32x4(yj), accEven)
		accOdd = broadcastF32x4Arch(x[j+1]).MulAdd(loadF32x4(unsafe.Add(yj, 4)), accOdd)
		accEven = broadcastF32x4Arch(x[j+2]).MulAdd(loadF32x4(unsafe.Add(yj, 8)), accEven)
		accOdd = broadcastF32x4Arch(x[j+3]).MulAdd(loadF32x4(unsafe.Add(yj, 12)), accOdd)
	}
	if j < length {
		accEven = broadcastF32x4Arch(x[j]).MulAdd(loadF32x4(unsafe.Add(yp, uintptr(j)*4)), accEven)
		j++
		if j < length {
			accOdd = broadcastF32x4Arch(x[j]).MulAdd(loadF32x4(unsafe.Add(yp, uintptr(j)*4)), accOdd)
			j++
			if j < length {
				accEven = broadcastF32x4Arch(x[j]).MulAdd(loadF32x4(unsafe.Add(yp, uintptr(j)*4)), accEven)
			}
		}
	}
	accEven.Add(accOdd).StoreArray(sum)
}

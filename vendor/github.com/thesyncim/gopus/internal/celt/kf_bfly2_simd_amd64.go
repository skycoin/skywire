//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"unsafe"

	"simd/archsimd"
)

// kfBfly2M4SIMD is kfBfly2M4Scalar with the four butterflies of each group
// held in real and imaginary vectors. Each lane keeps the scalar operand order,
// and the lanes that take no twiddle product pick their inputs with blends.
func kfBfly2M4SIMD(fout []kissCpx, N int) {
	if N <= 0 {
		return
	}
	if !archsimd.X86.AVX() {
		kfBfly2M4Scalar(fout, N)
		return
	}
	_ = fout[8*N-1]
	p := unsafe.Pointer(unsafe.SliceData(fout))
	tw := broadcastF32x4Arch(kfBfly2M4Twiddle)
	lane0 := archsimd.LoadInt32x4Array(&[4]int32{-1, 0, 0, 0}).ToMask()
	lane1 := archsimd.LoadInt32x4Array(&[4]int32{0, -1, 0, 0}).ToMask()
	lane2 := archsimd.LoadInt32x4Array(&[4]int32{0, 0, -1, 0}).ToMask()
	lane3 := archsimd.LoadInt32x4Array(&[4]int32{0, 0, 0, -1}).ToMask()
	fusedGroups := N &^ 3
	for group := 0; group < N; group++ {
		fr, fi := bflyLoadCpx4AMD64(p)
		r, i := bflyLoadCpx4AMD64(unsafe.Add(p, 32))
		minusR, minusI, plusR, plusI := bfly2M4GroupOutputsAMD64(fr, fi, r, i, tw, lane0, lane1, lane2, lane3, group < fusedGroups)
		bflyStoreCpx4AMD64(unsafe.Add(p, 32), minusR, minusI)
		bflyStoreCpx4AMD64(p, plusR, plusI)
		p = unsafe.Add(p, 64)
	}
}

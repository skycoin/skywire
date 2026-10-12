//go:build !amd64 || nosimd || purego || !goexperiment.simd

package celt

const useX86PVQSearchSSE2 = false

func opPVQSearchScratchNormX86SSE2(x []celtNorm, k int, iyBuf *[]int32, signxBuf *[]byte, yBuf *[]float32, absXBuf *[]float32, absInput bool) ([]int32, opusVal16) {
	return nil, 0
}

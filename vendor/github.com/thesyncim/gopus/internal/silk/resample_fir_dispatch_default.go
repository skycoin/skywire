//go:build !arm64 || !goexperiment.simd || nosimd || purego

package silk

func firInterpol21846CoreDispatch(dst []int16, buf []int16, nOut int) {
	firInterpol21846CoreGo(dst, buf, nOut)
}

func firInterpol32768CoreDispatch(dst []int16, buf []int16, nOut int) {
	firInterpol32768CoreGo(dst, buf, nOut)
}

func firInterpol43691CoreDispatch(dst []int16, buf []int16, nOut int) {
	firInterpol43691CoreGo(dst, buf, nOut)
}

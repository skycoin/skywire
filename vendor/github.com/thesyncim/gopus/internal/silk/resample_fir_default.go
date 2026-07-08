//go:build !arm64 || purego

package silk

func firInterpol21846Core(dst []int16, buf []int16, nOut int) {
	firInterpol21846CoreGo(dst, buf, nOut)
}

func firInterpol32768Core(dst []int16, buf []int16, nOut int) {
	firInterpol32768CoreGo(dst, buf, nOut)
}

func firInterpol43691Core(dst []int16, buf []int16, nOut int) {
	firInterpol43691CoreGo(dst, buf, nOut)
}

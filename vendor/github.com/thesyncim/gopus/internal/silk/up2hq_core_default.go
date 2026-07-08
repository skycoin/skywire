//go:build !arm64 || race || purego

package silk

func up2HQCore(out []int16, in []int16, sIIR *[6]int32) {
	up2HQCoreGo(out, in, sIIR)
}

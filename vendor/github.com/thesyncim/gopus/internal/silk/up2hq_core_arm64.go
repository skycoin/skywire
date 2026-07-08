//go:build arm64 && !race && !purego

package silk

//go:noescape
func up2HQCore(out []int16, in []int16, sIIR *[6]int32)

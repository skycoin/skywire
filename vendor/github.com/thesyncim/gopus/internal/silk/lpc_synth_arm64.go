//go:build arm64 && !purego

package silk

//go:noescape
func synthesizeLPCOrder16Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int)

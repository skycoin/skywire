//go:build !gopus_dred && !gopus_osce

package silk

func deepPLCSkipsRecoveryRamp(int32) bool { return false }

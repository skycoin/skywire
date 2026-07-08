//go:build arm64 && darwin

package cpufeat

import "syscall"

func init() {
	ARM64.HasASIMD = enabled("hw.optional.arm.AdvSIMD") || enabled("hw.optional.neon")
	ARM64.HasDotProd = enabled("hw.optional.arm.FEAT_DotProd")
	ARM64.HasFCMA = enabled("hw.optional.arm.FEAT_FCMA")
	ARM64.HasFHM = enabled("hw.optional.arm.FEAT_FHM") || enabled("hw.optional.armv8_2_fhm")
	ARM64.HasBF16 = enabled("hw.optional.arm.FEAT_BF16")
	ARM64.HasI8MM = enabled("hw.optional.arm.FEAT_I8MM")
	ARM64.HasSME = enabled("hw.optional.arm.FEAT_SME")
	ARM64.HasSMEF64F64 = enabled("hw.optional.arm.FEAT_SME_F64F64")
}

func enabled(name string) bool {
	v, err := syscall.SysctlUint32(name)
	return err == nil && v != 0
}

//go:build gopus_fixed_point && !gopus_qext

package fixedpoint

// celtExp2DbFrac implements the FIXED_POINT non-QEXT macro from mathops.h:
// SHL32(celt_exp2_frac(PSHR32(x, DB_SHIFT-10)), 14).
func celtExp2DbFrac(x int32) int32 {
	return celtExp2DbFracQ15(x)
}

// celtExp2Db implements the FIXED_POINT non-QEXT celt_exp2_db macro.
func celtExp2Db(x int32) int32 {
	return celtExp2DbQ15(x)
}

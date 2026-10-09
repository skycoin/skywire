//go:build gopus_fixed_point && !gopus_qext

package fixedpoint

// celtLog2DB matches the non-QEXT FIXED_POINT macro in celt/mathops.h:
// SHL32(EXTEND32(celt_log2(x)), DB_SHIFT-10).
func celtLog2DB(x int32) int32 {
	return int32(CeltLog2(x)) << (dbShift - 10)
}

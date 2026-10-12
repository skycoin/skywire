//go:build gopus_fixed_point

package fixedpoint

// celtExp2DbFracQ15 matches celt/mathops.h celt_exp2_db_frac in the
// FIXED_POINT build without ENABLE_QEXT. The QEXT build also uses this explicit
// helper for CELTDecoder alongside the selected Q31 polynomial.
func celtExp2DbFracQ15(x int32) int32 {
	q10 := int16(pshr32(x, dbShift-10))
	return int32(CeltExp2Frac(q10)) << 14
}

// celtExp2DbQ15 matches celt/mathops.h celt_exp2_db in the FIXED_POINT build
// without ENABLE_QEXT.
func celtExp2DbQ15(x int32) int32 {
	return CeltExp2(int16(pshr32(x, dbShift-10)))
}

//go:build gopus_fixed_point && gopus_qext

package fixedpoint

// celtExp2DbFrac ports celt/mathops.h celt_exp2_db_frac for FIXED_POINT and
// ENABLE_QEXT. Its polynomial evaluates the Q29 input into a Q28 result; the
// non-QEXT Q14 lookup approximation differs by enough to change final synthesis.
func celtExp2DbFrac(x int32) int32 {
	const (
		a0 int32 = 268435440  // Q28
		a1 int32 = 744267456  // Q30
		a2 int32 = 1031451904 // Q32
		a3 int32 = 959088832  // Q34
		a4 int32 = 617742720  // Q36
		a5 int32 = 516104352  // Q38
	)
	xQ29 := shl32(x, 29-dbShift)
	tmp := add32(a4, mult32x32q31(xQ29, a5))
	tmp = add32(a3, mult32x32q31(xQ29, tmp))
	tmp = add32(a2, mult32x32q31(xQ29, tmp))
	tmp = add32(a1, mult32x32q31(xQ29, tmp))
	return add32(a0, mult32x32q31(xQ29, tmp))
}

// celtExp2Db ports celt/mathops.h celt_exp2_db for FIXED_POINT+ENABLE_QEXT.
func celtExp2Db(x int32) int32 {
	integer := x >> dbShift
	if integer > 14 {
		return 0x7f000000
	}
	if integer <= -17 {
		return 0
	}
	frac := celtExp2DbFrac(x - (integer << dbShift))
	return vshr32(frac, int(-integer+28-16))
}

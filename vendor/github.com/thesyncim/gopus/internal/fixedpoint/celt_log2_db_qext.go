//go:build gopus_fixed_point && gopus_qext

package fixedpoint

// celtLog2DB matches the ENABLE_QEXT celt_log2_db polynomial in
// celt/mathops.h. It normalizes the Q14 input into Q29, selects the matching
// coefficient set, and evaluates the Q28 polynomial into Q24 output.
func celtLog2DB(x int32) int32 {
	if x == 0 {
		return -32 << dbShift
	}

	integer := int32(CeltILog2(x)) - 14
	mantissa := vshr32(x, int(integer)-15)
	coeffIndex := int(uint32(mantissa>>26) & 7)
	mantissa = mult32x32q31(mantissa, qextLog2XNorm[coeffIndex]) - 285212672

	tmp := mult32x32q31(mantissa, -610217024)
	tmp = mult32x32q31(mantissa, 107903336+tmp)
	tmp = shl32(mult32x32q31(mantissa, -21440512+tmp), 5)
	tmp = mult32x32q31(mantissa, 182244800+tmp)
	return qextLog2YNorm[coeffIndex] + shl32(integer, dbShift) + 1467383 + tmp
}

var qextLog2XNorm = [8]int32{
	1073741824, 954437184, 858993472, 780903168,
	715827904, 660764224, 613566784, 572662336,
}

var qextLog2YNorm = [8]int32{
	0, 2850868, 5401057, 7707983,
	9814042, 11751428, 13545168, 15215099,
}

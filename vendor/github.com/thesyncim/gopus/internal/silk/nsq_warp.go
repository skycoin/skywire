package silk

// warpedARFeedback24 computes 24-tap warped AR noise shaping feedback.
// Sequential dependencies prevent SIMD parallelism.
func warpedARFeedback24(sAR2Q14 *[maxShapeLpcOrder]int32, diffQ14 int32, arShpQ13 *[24]int16, warpQ16 int32) int32 {
	sAR := sAR2Q14
	w := int64(warpQ16)
	_ = sAR[23] // BCE

	tmp2 := diffQ14 + int32((int64(sAR[0])*w)>>16)
	tmp1 := sAR[0] + int32((int64(sAR[1]-tmp2)*w)>>16)
	sAR[0] = tmp2
	acc := int32(12) + int32((int64(tmp2)*int64(arShpQ13[0]))>>16)

	// j=2
	tmp2 = sAR[1] + int32((int64(sAR[2]-tmp1)*w)>>16)
	sAR[1] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[1])) >> 16)
	tmp1 = sAR[2] + int32((int64(sAR[3]-tmp2)*w)>>16)
	sAR[2] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[2])) >> 16)
	// j=4
	tmp2 = sAR[3] + int32((int64(sAR[4]-tmp1)*w)>>16)
	sAR[3] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[3])) >> 16)
	tmp1 = sAR[4] + int32((int64(sAR[5]-tmp2)*w)>>16)
	sAR[4] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[4])) >> 16)
	// j=6
	tmp2 = sAR[5] + int32((int64(sAR[6]-tmp1)*w)>>16)
	sAR[5] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[5])) >> 16)
	tmp1 = sAR[6] + int32((int64(sAR[7]-tmp2)*w)>>16)
	sAR[6] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[6])) >> 16)
	// j=8
	tmp2 = sAR[7] + int32((int64(sAR[8]-tmp1)*w)>>16)
	sAR[7] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[7])) >> 16)
	tmp1 = sAR[8] + int32((int64(sAR[9]-tmp2)*w)>>16)
	sAR[8] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[8])) >> 16)
	// j=10
	tmp2 = sAR[9] + int32((int64(sAR[10]-tmp1)*w)>>16)
	sAR[9] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[9])) >> 16)
	tmp1 = sAR[10] + int32((int64(sAR[11]-tmp2)*w)>>16)
	sAR[10] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[10])) >> 16)
	// j=12
	tmp2 = sAR[11] + int32((int64(sAR[12]-tmp1)*w)>>16)
	sAR[11] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[11])) >> 16)
	tmp1 = sAR[12] + int32((int64(sAR[13]-tmp2)*w)>>16)
	sAR[12] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[12])) >> 16)
	// j=14
	tmp2 = sAR[13] + int32((int64(sAR[14]-tmp1)*w)>>16)
	sAR[13] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[13])) >> 16)
	tmp1 = sAR[14] + int32((int64(sAR[15]-tmp2)*w)>>16)
	sAR[14] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[14])) >> 16)
	// j=16
	tmp2 = sAR[15] + int32((int64(sAR[16]-tmp1)*w)>>16)
	sAR[15] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[15])) >> 16)
	tmp1 = sAR[16] + int32((int64(sAR[17]-tmp2)*w)>>16)
	sAR[16] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[16])) >> 16)
	// j=18
	tmp2 = sAR[17] + int32((int64(sAR[18]-tmp1)*w)>>16)
	sAR[17] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[17])) >> 16)
	tmp1 = sAR[18] + int32((int64(sAR[19]-tmp2)*w)>>16)
	sAR[18] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[18])) >> 16)
	// j=20
	tmp2 = sAR[19] + int32((int64(sAR[20]-tmp1)*w)>>16)
	sAR[19] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[19])) >> 16)
	tmp1 = sAR[20] + int32((int64(sAR[21]-tmp2)*w)>>16)
	sAR[20] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[20])) >> 16)
	// j=22
	tmp2 = sAR[21] + int32((int64(sAR[22]-tmp1)*w)>>16)
	sAR[21] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[21])) >> 16)
	tmp1 = sAR[22] + int32((int64(sAR[23]-tmp2)*w)>>16)
	sAR[22] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[22])) >> 16)
	// final tap
	sAR[23] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[23])) >> 16)

	return acc
}

// warpedARFeedback24States4 computes the 24-tap warped AR feedback for the
// four delayed-decision states used at libopus complexity >= 8. Each state's
// arithmetic order matches warpedARFeedback24; the states are only interleaved.
func warpedARFeedback24States4(psDelDec []nsqDelDecState, arShpQ13 *[24]int16, warpQ16 int32, out *[maxDelDecStates]int32) {
	_ = psDelDec[3]
	s0 := &psDelDec[0].sAR2Q14
	s1 := &psDelDec[1].sAR2Q14
	s2 := &psDelDec[2].sAR2Q14
	s3 := &psDelDec[3].sAR2Q14
	_ = s0[23]
	_ = s1[23]
	_ = s2[23]
	_ = s3[23]

	w := int64(warpQ16)
	c0 := int64(arShpQ13[0])

	tmp20 := psDelDec[0].diffQ14 + int32((int64(s0[0])*w)>>16)
	tmp21 := psDelDec[1].diffQ14 + int32((int64(s1[0])*w)>>16)
	tmp22 := psDelDec[2].diffQ14 + int32((int64(s2[0])*w)>>16)
	tmp23 := psDelDec[3].diffQ14 + int32((int64(s3[0])*w)>>16)

	tmp10 := s0[0] + int32((int64(s0[1]-tmp20)*w)>>16)
	tmp11 := s1[0] + int32((int64(s1[1]-tmp21)*w)>>16)
	tmp12 := s2[0] + int32((int64(s2[1]-tmp22)*w)>>16)
	tmp13 := s3[0] + int32((int64(s3[1]-tmp23)*w)>>16)

	s0[0] = tmp20
	s1[0] = tmp21
	s2[0] = tmp22
	s3[0] = tmp23

	acc0 := int32(12) + int32((int64(tmp20)*c0)>>16)
	acc1 := int32(12) + int32((int64(tmp21)*c0)>>16)
	acc2 := int32(12) + int32((int64(tmp22)*c0)>>16)
	acc3 := int32(12) + int32((int64(tmp23)*c0)>>16)

	for j := 2; j < 24; j += 2 {
		cPrev := int64(arShpQ13[j-1])
		cCur := int64(arShpQ13[j])

		tmp20 = s0[j-1] + int32((int64(s0[j]-tmp10)*w)>>16)
		tmp21 = s1[j-1] + int32((int64(s1[j]-tmp11)*w)>>16)
		tmp22 = s2[j-1] + int32((int64(s2[j]-tmp12)*w)>>16)
		tmp23 = s3[j-1] + int32((int64(s3[j]-tmp13)*w)>>16)
		s0[j-1] = tmp10
		s1[j-1] = tmp11
		s2[j-1] = tmp12
		s3[j-1] = tmp13
		acc0 += int32((int64(tmp10) * cPrev) >> 16)
		acc1 += int32((int64(tmp11) * cPrev) >> 16)
		acc2 += int32((int64(tmp12) * cPrev) >> 16)
		acc3 += int32((int64(tmp13) * cPrev) >> 16)

		tmp10 = s0[j] + int32((int64(s0[j+1]-tmp20)*w)>>16)
		tmp11 = s1[j] + int32((int64(s1[j+1]-tmp21)*w)>>16)
		tmp12 = s2[j] + int32((int64(s2[j+1]-tmp22)*w)>>16)
		tmp13 = s3[j] + int32((int64(s3[j+1]-tmp23)*w)>>16)
		s0[j] = tmp20
		s1[j] = tmp21
		s2[j] = tmp22
		s3[j] = tmp23
		acc0 += int32((int64(tmp20) * cCur) >> 16)
		acc1 += int32((int64(tmp21) * cCur) >> 16)
		acc2 += int32((int64(tmp22) * cCur) >> 16)
		acc3 += int32((int64(tmp23) * cCur) >> 16)
	}

	cLast := int64(arShpQ13[23])
	s0[23] = tmp10
	s1[23] = tmp11
	s2[23] = tmp12
	s3[23] = tmp13
	acc0 += int32((int64(tmp10) * cLast) >> 16)
	acc1 += int32((int64(tmp11) * cLast) >> 16)
	acc2 += int32((int64(tmp12) * cLast) >> 16)
	acc3 += int32((int64(tmp13) * cLast) >> 16)

	out[0] = acc0
	out[1] = acc1
	out[2] = acc2
	out[3] = acc3
}

// warpedARFeedback16 computes 16-tap warped AR noise shaping feedback.
// Sequential dependencies prevent SIMD parallelism.
func warpedARFeedback16(sAR2Q14 *[maxShapeLpcOrder]int32, diffQ14 int32, arShpQ13 *[16]int16, warpQ16 int32) int32 {
	sAR := sAR2Q14
	w := int64(warpQ16)
	_ = sAR[15] // BCE

	tmp2 := diffQ14 + int32((int64(sAR[0])*w)>>16)
	tmp1 := sAR[0] + int32((int64(sAR[1]-tmp2)*w)>>16)
	sAR[0] = tmp2
	acc := int32(8) + int32((int64(tmp2)*int64(arShpQ13[0]))>>16)

	// j=2
	tmp2 = sAR[1] + int32((int64(sAR[2]-tmp1)*w)>>16)
	sAR[1] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[1])) >> 16)
	tmp1 = sAR[2] + int32((int64(sAR[3]-tmp2)*w)>>16)
	sAR[2] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[2])) >> 16)
	// j=4
	tmp2 = sAR[3] + int32((int64(sAR[4]-tmp1)*w)>>16)
	sAR[3] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[3])) >> 16)
	tmp1 = sAR[4] + int32((int64(sAR[5]-tmp2)*w)>>16)
	sAR[4] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[4])) >> 16)
	// j=6
	tmp2 = sAR[5] + int32((int64(sAR[6]-tmp1)*w)>>16)
	sAR[5] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[5])) >> 16)
	tmp1 = sAR[6] + int32((int64(sAR[7]-tmp2)*w)>>16)
	sAR[6] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[6])) >> 16)
	// j=8
	tmp2 = sAR[7] + int32((int64(sAR[8]-tmp1)*w)>>16)
	sAR[7] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[7])) >> 16)
	tmp1 = sAR[8] + int32((int64(sAR[9]-tmp2)*w)>>16)
	sAR[8] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[8])) >> 16)
	// j=10
	tmp2 = sAR[9] + int32((int64(sAR[10]-tmp1)*w)>>16)
	sAR[9] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[9])) >> 16)
	tmp1 = sAR[10] + int32((int64(sAR[11]-tmp2)*w)>>16)
	sAR[10] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[10])) >> 16)
	// j=12
	tmp2 = sAR[11] + int32((int64(sAR[12]-tmp1)*w)>>16)
	sAR[11] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[11])) >> 16)
	tmp1 = sAR[12] + int32((int64(sAR[13]-tmp2)*w)>>16)
	sAR[12] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[12])) >> 16)
	// j=14
	tmp2 = sAR[13] + int32((int64(sAR[14]-tmp1)*w)>>16)
	sAR[13] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[13])) >> 16)
	tmp1 = sAR[14] + int32((int64(sAR[15]-tmp2)*w)>>16)
	sAR[14] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[14])) >> 16)
	// final tap
	sAR[15] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[15])) >> 16)

	return acc
}

package silk

// shortTermPrediction16 computes 16-tap LPC prediction.
// Returns 8 + sum((sLPCQ14[idx-k] * int16(aQ12[k])) >> 16) for k=0..15.
func shortTermPrediction16(sLPCQ14 []int32, idx int, aQ12 []int16) int32 {
	_ = sLPCQ14[idx]
	_ = sLPCQ14[idx-15]
	_ = aQ12[15]
	out := int32(8) // order>>1 = 16>>1
	out = silk_SMLAWB(out, sLPCQ14[idx-0], int32(aQ12[0]))
	out = silk_SMLAWB(out, sLPCQ14[idx-1], int32(aQ12[1]))
	out = silk_SMLAWB(out, sLPCQ14[idx-2], int32(aQ12[2]))
	out = silk_SMLAWB(out, sLPCQ14[idx-3], int32(aQ12[3]))
	out = silk_SMLAWB(out, sLPCQ14[idx-4], int32(aQ12[4]))
	out = silk_SMLAWB(out, sLPCQ14[idx-5], int32(aQ12[5]))
	out = silk_SMLAWB(out, sLPCQ14[idx-6], int32(aQ12[6]))
	out = silk_SMLAWB(out, sLPCQ14[idx-7], int32(aQ12[7]))
	out = silk_SMLAWB(out, sLPCQ14[idx-8], int32(aQ12[8]))
	out = silk_SMLAWB(out, sLPCQ14[idx-9], int32(aQ12[9]))
	out = silk_SMLAWB(out, sLPCQ14[idx-10], int32(aQ12[10]))
	out = silk_SMLAWB(out, sLPCQ14[idx-11], int32(aQ12[11]))
	out = silk_SMLAWB(out, sLPCQ14[idx-12], int32(aQ12[12]))
	out = silk_SMLAWB(out, sLPCQ14[idx-13], int32(aQ12[13]))
	out = silk_SMLAWB(out, sLPCQ14[idx-14], int32(aQ12[14]))
	out = silk_SMLAWB(out, sLPCQ14[idx-15], int32(aQ12[15]))
	return out
}

func shortTermPrediction16State(sLPCQ14 *[maxSubFrameLength + nsqLpcBufLength]int32, idx int, aQ12 *[16]int16) int32 {
	lpc := sLPCQ14[idx-15 : idx+1 : idx+1]
	// Four independent accumulator chains break the serial SMLAWB dependency so
	// the out-of-order pipeline can issue the per-tap products in parallel.
	// silk_SMLAWB sums int32 terms; two's-complement int32 addition is
	// associative, so the regrouped sum is bit-identical to the serial chain.
	s0 := int32(8) + int32((int64(lpc[15])*int64(aQ12[0]))>>16)
	s1 := int32((int64(lpc[14]) * int64(aQ12[1])) >> 16)
	s2 := int32((int64(lpc[13]) * int64(aQ12[2])) >> 16)
	s3 := int32((int64(lpc[12]) * int64(aQ12[3])) >> 16)
	s0 += int32((int64(lpc[11]) * int64(aQ12[4])) >> 16)
	s1 += int32((int64(lpc[10]) * int64(aQ12[5])) >> 16)
	s2 += int32((int64(lpc[9]) * int64(aQ12[6])) >> 16)
	s3 += int32((int64(lpc[8]) * int64(aQ12[7])) >> 16)
	s0 += int32((int64(lpc[7]) * int64(aQ12[8])) >> 16)
	s1 += int32((int64(lpc[6]) * int64(aQ12[9])) >> 16)
	s2 += int32((int64(lpc[5]) * int64(aQ12[10])) >> 16)
	s3 += int32((int64(lpc[4]) * int64(aQ12[11])) >> 16)
	s0 += int32((int64(lpc[3]) * int64(aQ12[12])) >> 16)
	s1 += int32((int64(lpc[2]) * int64(aQ12[13])) >> 16)
	s2 += int32((int64(lpc[1]) * int64(aQ12[14])) >> 16)
	s3 += int32((int64(lpc[0]) * int64(aQ12[15])) >> 16)
	return (s0 + s1) + (s2 + s3)
}

// shortTermPrediction10 computes 10-tap LPC prediction.
// Returns 5 + sum((sLPCQ14[idx-k] * int16(aQ12[k])) >> 16) for k=0..9.
func shortTermPrediction10(sLPCQ14 []int32, idx int, aQ12 []int16) int32 {
	_ = sLPCQ14[idx]
	_ = sLPCQ14[idx-9]
	_ = aQ12[9]
	out := int32(5) // order>>1 = 10>>1
	out = silk_SMLAWB(out, sLPCQ14[idx-0], int32(aQ12[0]))
	out = silk_SMLAWB(out, sLPCQ14[idx-1], int32(aQ12[1]))
	out = silk_SMLAWB(out, sLPCQ14[idx-2], int32(aQ12[2]))
	out = silk_SMLAWB(out, sLPCQ14[idx-3], int32(aQ12[3]))
	out = silk_SMLAWB(out, sLPCQ14[idx-4], int32(aQ12[4]))
	out = silk_SMLAWB(out, sLPCQ14[idx-5], int32(aQ12[5]))
	out = silk_SMLAWB(out, sLPCQ14[idx-6], int32(aQ12[6]))
	out = silk_SMLAWB(out, sLPCQ14[idx-7], int32(aQ12[7]))
	out = silk_SMLAWB(out, sLPCQ14[idx-8], int32(aQ12[8]))
	out = silk_SMLAWB(out, sLPCQ14[idx-9], int32(aQ12[9]))
	return out
}

func shortTermPrediction10State(sLPCQ14 *[maxSubFrameLength + nsqLpcBufLength]int32, idx int, aQ12 *[10]int16) int32 {
	lpc := sLPCQ14[idx-9 : idx+1 : idx+1]
	out := int32(5)
	out = silk_SMLAWB(out, lpc[9], int32(aQ12[0]))
	out = silk_SMLAWB(out, lpc[8], int32(aQ12[1]))
	out = silk_SMLAWB(out, lpc[7], int32(aQ12[2]))
	out = silk_SMLAWB(out, lpc[6], int32(aQ12[3]))
	out = silk_SMLAWB(out, lpc[5], int32(aQ12[4]))
	out = silk_SMLAWB(out, lpc[4], int32(aQ12[5]))
	out = silk_SMLAWB(out, lpc[3], int32(aQ12[6]))
	out = silk_SMLAWB(out, lpc[2], int32(aQ12[7]))
	out = silk_SMLAWB(out, lpc[1], int32(aQ12[8]))
	out = silk_SMLAWB(out, lpc[0], int32(aQ12[9]))
	return out
}

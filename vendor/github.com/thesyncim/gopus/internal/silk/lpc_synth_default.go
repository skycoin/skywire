//go:build !arm64 || !goexperiment.simd || nosimd || purego

package silk

// synthesizeLPCOrder16Core is the order-16 LPC synthesis loop of libopus
// silk/decode_core.c silk_decode_core. The prediction is a wrapping int32 sum
// of silk_SMLAWB products, so its terms may be added in any order. The loop
// sums the fifteen taps on older outputs as a balanced tree and adds the tap on
// the newest output last, which keeps that output's feedback path short.
func synthesizeLPCOrder16Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	if subfrLength <= 0 {
		return
	}
	_ = A_Q12[15]
	c0 := int64(A_Q12[0])
	c1 := int64(A_Q12[1])
	c2 := int64(A_Q12[2])
	c3 := int64(A_Q12[3])
	c4 := int64(A_Q12[4])
	c5 := int64(A_Q12[5])
	c6 := int64(A_Q12[6])
	c7 := int64(A_Q12[7])
	c8 := int64(A_Q12[8])
	c9 := int64(A_Q12[9])
	c10 := int64(A_Q12[10])
	c11 := int64(A_Q12[11])
	c12 := int64(A_Q12[12])
	c13 := int64(A_Q12[13])
	c14 := int64(A_Q12[14])
	c15 := int64(A_Q12[15])

	presQ14 = presQ14[:subfrLength]
	pxq = pxq[:subfrLength]
	win := sLPC[:maxLPCOrder+subfrLength]
	prev := win[maxLPCOrder-1]
	for i := range presQ14 {
		// h[15-j] is the output j+1 samples back; h[15] is prev.
		h := (*[maxLPCOrder + 1]int32)(win)
		win = win[1:]
		a := int32((int64(h[0])*c15)>>16) + int32((int64(h[1])*c14)>>16)
		b := int32((int64(h[2])*c13)>>16) + int32((int64(h[3])*c12)>>16)
		c := int32((int64(h[4])*c11)>>16) + int32((int64(h[5])*c10)>>16)
		d := int32((int64(h[6])*c9)>>16) + int32((int64(h[7])*c8)>>16)
		e := int32((int64(h[8])*c7)>>16) + int32((int64(h[9])*c6)>>16)
		f := int32((int64(h[10])*c5)>>16) + int32((int64(h[11])*c4)>>16)
		g := int32((int64(h[12])*c3)>>16) + int32((int64(h[13])*c2)>>16)
		older := ((a + b) + (c + d)) + ((e + f) + (g + int32((int64(h[14])*c1)>>16)))
		lpcPredQ10 := older + int32(maxLPCOrder>>1) + int32((int64(prev)*c0)>>16)

		s := silkAddSat32(presQ14[i], lShiftSAT32By4(lpcPredQ10))
		h[maxLPCOrder] = s
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(s, gainQ10), 8))
		prev = s
	}
}

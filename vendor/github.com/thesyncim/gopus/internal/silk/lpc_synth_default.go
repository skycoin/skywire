//go:build !arm64 || purego

package silk

func synthesizeLPCOrder16Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	c0 := int32(A_Q12[0])
	c1 := int32(A_Q12[1])
	c2 := int32(A_Q12[2])
	c3 := int32(A_Q12[3])
	c4 := int32(A_Q12[4])
	c5 := int32(A_Q12[5])
	c6 := int32(A_Q12[6])
	c7 := int32(A_Q12[7])
	c8 := int32(A_Q12[8])
	c9 := int32(A_Q12[9])
	c10 := int32(A_Q12[10])
	c11 := int32(A_Q12[11])
	c12 := int32(A_Q12[12])
	c13 := int32(A_Q12[13])
	c14 := int32(A_Q12[14])
	c15 := int32(A_Q12[15])

	v0 := sLPC[maxLPCOrder-1]
	v1 := sLPC[maxLPCOrder-2]
	v2 := sLPC[maxLPCOrder-3]
	v3 := sLPC[maxLPCOrder-4]
	v4 := sLPC[maxLPCOrder-5]
	v5 := sLPC[maxLPCOrder-6]
	v6 := sLPC[maxLPCOrder-7]
	v7 := sLPC[maxLPCOrder-8]
	v8 := sLPC[maxLPCOrder-9]
	v9 := sLPC[maxLPCOrder-10]
	v10 := sLPC[maxLPCOrder-11]
	v11 := sLPC[maxLPCOrder-12]
	v12 := sLPC[maxLPCOrder-13]
	v13 := sLPC[maxLPCOrder-14]
	v14 := sLPC[maxLPCOrder-15]
	v15 := sLPC[maxLPCOrder-16]

	sIdx := maxLPCOrder
	for i := 0; i < subfrLength; i++ {
		lpcPredQ10 := int32(maxLPCOrder >> 1)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v0, c0)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v1, c1)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v2, c2)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v3, c3)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v4, c4)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v5, c5)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v6, c6)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v7, c7)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v8, c8)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v9, c9)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v10, c10)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v11, c11)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v12, c12)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v13, c13)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v14, c14)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v15, c15)

		s := silkAddSat32(presQ14[i], lShiftSAT32By4(lpcPredQ10))
		sLPC[sIdx] = s
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(s, gainQ10), 8))
		sIdx++

		v15 = v14
		v14 = v13
		v13 = v12
		v12 = v11
		v11 = v10
		v10 = v9
		v9 = v8
		v8 = v7
		v7 = v6
		v6 = v5
		v5 = v4
		v4 = v3
		v3 = v2
		v2 = v1
		v1 = v0
		v0 = s
	}
}

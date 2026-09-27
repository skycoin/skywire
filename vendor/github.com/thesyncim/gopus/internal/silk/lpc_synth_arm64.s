//go:build arm64 && !purego

#include "textflag.h"

// func synthesizeLPCOrder16Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int)
//
// Order-16 LPC synthesis. Each silk_SMLAWB term is ((state * coef) >> 16)
// truncated to int32; int32 wrapping addition is associative, so the 16-term
// accumulation computes as four NEON lanes of SMULL/SHRN partial terms,
// combined with vector adds and a final across-lanes ADDV — bit-identical to
// the sequential scalar sum. States live in V16..V19 (memory order, newest
// last) and rotate with EXT; the saturating sample epilogue stays scalar.
TEXT ·synthesizeLPCOrder16Core(SB), NOSPLIT, $0-112
	MOVD  sLPC_base+0(FP), R0
	MOVD  A_Q12_base+24(FP), R1
	MOVD  presQ14_base+48(FP), R2
	MOVD  pxq_base+72(FP), R3
	MOVW  gainQ10+96(FP), R4
	MOVD  subfrLength+104(FP), R5

	CBZ   R5, lpc16_done

	// States in memory order: V16 = sLPC[0..3] = [v15,v14,v13,v12] ... V19 =
	// sLPC[12..15] = [v3,v2,v1,v0].
	VLD1.P 32(R0), [V16.S4, V17.S4]
	VLD1.P 32(R0), [V18.S4, V19.S4]

	// Coefficients into matching lanes: V4 = [c15,c14,c13,c12] ... V7 =
	// [c3,c2,c1,c0].
	MOVH  30(R1), R7
	VMOV  R7, V4.S[0]
	MOVH  28(R1), R7
	VMOV  R7, V4.S[1]
	MOVH  26(R1), R7
	VMOV  R7, V4.S[2]
	MOVH  24(R1), R7
	VMOV  R7, V4.S[3]
	MOVH  22(R1), R7
	VMOV  R7, V5.S[0]
	MOVH  20(R1), R7
	VMOV  R7, V5.S[1]
	MOVH  18(R1), R7
	VMOV  R7, V5.S[2]
	MOVH  16(R1), R7
	VMOV  R7, V5.S[3]
	MOVH  14(R1), R7
	VMOV  R7, V6.S[0]
	MOVH  12(R1), R7
	VMOV  R7, V6.S[1]
	MOVH  10(R1), R7
	VMOV  R7, V6.S[2]
	MOVH  8(R1), R7
	VMOV  R7, V6.S[3]
	MOVH  6(R1), R7
	VMOV  R7, V7.S[0]
	MOVH  4(R1), R7
	VMOV  R7, V7.S[1]
	MOVH  2(R1), R7
	VMOV  R7, V7.S[2]
	MOVH  (R1), R7
	VMOV  R7, V7.S[3]

lpc16_loop:
	// lpcPredQ10 = 8 + sum_k ((v_k * c_k) >> 16), int32 wrapping.
	WORD $0x0EA4C214 // SMULL  V20.2D, V16.2S, V4.2S
	WORD $0x4EA4C215 // SMULL2 V21.2D, V16.4S, V4.4S
	WORD $0x0F308694 // SHRN   V20.2S, V20.2D, #16
	WORD $0x4F3086B4 // SHRN2  V20.4S, V21.2D, #16
	WORD $0x0EA5C236 // SMULL  V22.2D, V17.2S, V5.2S
	WORD $0x4EA5C237 // SMULL2 V23.2D, V17.4S, V5.4S
	WORD $0x0F3086D6 // SHRN   V22.2S, V22.2D, #16
	WORD $0x4F3086F6 // SHRN2  V22.4S, V23.2D, #16
	WORD $0x0EA6C258 // SMULL  V24.2D, V18.2S, V6.2S
	WORD $0x4EA6C259 // SMULL2 V25.2D, V18.4S, V6.4S
	WORD $0x0F308718 // SHRN   V24.2S, V24.2D, #16
	WORD $0x4F308738 // SHRN2  V24.4S, V25.2D, #16
	WORD $0x0EA7C27A // SMULL  V26.2D, V19.2S, V7.2S
	WORD $0x4EA7C27B // SMULL2 V27.2D, V19.4S, V7.4S
	WORD $0x0F30875A // SHRN   V26.2S, V26.2D, #16
	WORD $0x4F30877A // SHRN2  V26.4S, V27.2D, #16
	WORD $0x4EB68694 // ADD V20.4S, V20.4S, V22.4S
	WORD $0x4EBA8718 // ADD V24.4S, V24.4S, V26.4S
	WORD $0x4EB88694 // ADD V20.4S, V20.4S, V24.4S
	VADDV V20.S4, V20
	VMOV  V20.S[0], R6
	ADDW  $8, R6, R6

	// s = silkAddSat32(presQ14[i], lShiftSAT32By4(lpcPredQ10))
	SXTW  R6, R6
	LSL   $4, R6, R25
	SXTW  R25, R7
	CMP   R7, R25
	BEQ   lpc16_add_pres

	MOVD $2147483647, R25
	CMP  $0, R6
	BGE  lpc16_add_pres
	MOVD $-2147483648, R25

lpc16_add_pres:
	MOVW.P 4(R2), R24
	ADDSW  R25, R24, R26
	BVC    lpc16_add_done
	MOVD   $2147483647, R7
	MOVD   $-2147483648, R26
	CSEL   MI, R7, R26, R26
lpc16_add_done:
	SXTW   R26, R26

	MOVW.P R26, 4(R0)

	// pxq[i] = SAT16(round(SMULWW(s, gainQ10) >> 8))
	MUL    R4, R26, R24
	ASR    $16, R24, R24
	SXTW   R24, R24
	ADD    $128, R24, R24
	ASR    $8, R24, R24

	MOVD   $32767, R7
	CMPW   R7, R24
	CSEL   GT, R7, R24, R24
	MOVD   $-32768, R7
	CMPW   R7, R24
	CSEL   LT, R7, R24, R24

	MOVH.P R24, 2(R3)

	// Rotate the state vectors one lane toward older and insert s last.
	VMOV R26, V28.S[0]
	VEXT $4, V17.B16, V16.B16, V16.B16
	VEXT $4, V18.B16, V17.B16, V17.B16
	VEXT $4, V19.B16, V18.B16, V18.B16
	VEXT $4, V28.B16, V19.B16, V19.B16

	SUBS  $1, R5
	BNE   lpc16_loop

lpc16_done:
	RET

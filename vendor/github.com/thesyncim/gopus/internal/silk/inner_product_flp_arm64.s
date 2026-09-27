//go:build arm64 && !purego

#include "textflag.h"

// func innerProductFLPArm64(a, b []float32, length int) float64
//
// Mirrors libopus silk/float/inner_product_FLP.c: inputs are silk_float
// (float32), while the result and accumulator are C double. The 4-sample body
// preserves the C expression order for each chunk:
//
//   result += ((p0 + p1) + p2) + p3
//
// Keeping the chunk sum separate from result is observable for wide-dynamic-
// range inputs and is pinned by the live libopus oracle.
TEXT ·innerProductFLPArm64(SB), NOSPLIT, $0-64
	MOVD a_base+0(FP), R0
	MOVD b_base+24(FP), R1
	MOVD length+48(FP), R2

	FMOVD ZR, F16 // result
	MOVD  ZR, R3  // i

loop4:
	SUB R3, R2, R4 // remaining = length - i
	CMP $4, R4
	BLT tail

	VLD1.P 16(R0), [V0.S4]
	VLD1.P 16(R1), [V1.S4]
	WORD   $0x0e617802 // FCVTL  V2.2D, V0.2S
	WORD   $0x4e617803 // FCVTL2 V3.2D, V0.4S
	WORD   $0x0e617824 // FCVTL  V4.2D, V1.2S
	WORD   $0x4e617825 // FCVTL2 V5.2D, V1.4S
	WORD   $0x6e64dc46 // FMUL V6.2D, V2.2D, V4.2D
	WORD   $0x6e65dc67 // FMUL V7.2D, V3.2D, V5.2D
	WORD   $0x7e70d8c8 // FADDP D8, V6.2D       (p0 + p1)
	FADDD  F7, F8, F8  // chunk = (p0 + p1) + p2
	WORD   $0x5e1804e9 // MOV D9, V7.D[1]       (p3)
	FADDD  F9, F8, F8  // chunk = ((p0 + p1) + p2) + p3
	FADDD  F8, F16, F16 // result += chunk

	ADD $4, R3
	B   loop4

tail:
	SUB R3, R2, R4 // remaining = length - i
	CBZ R4, done

	FMOVS  (R0), F0
	FMOVS  (R1), F1
	FCVTSD F0, F0
	FCVTSD F1, F1
	FMULD  F1, F0, F8
	FADDD  F8, F16, F16

	ADD $4, R0
	ADD $4, R1
	ADD $1, R3
	B   tail

done:
	FMOVD F16, ret+56(FP)
	RET

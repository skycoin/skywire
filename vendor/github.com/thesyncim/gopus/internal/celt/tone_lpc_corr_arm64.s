//go:build arm64 && !purego

#include "textflag.h"

// func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32)
//
// Computes three float32 correlations for toneLPC using a 4-wide NEON FMA loop
// with scalar cleanup for the tail.
//
// WORD-encoded instructions:
//   FADDP V0.4S, V16.4S, V16.4S = 0x6E30D600
//   FADDP V0.4S, V17.4S, V17.4S = 0x6E31D620
//   FADDP V0.4S, V18.4S, V18.4S = 0x6E32D640
//   FADDP V0.4S, V0.4S, V0.4S   = 0x6E20D400
TEXT ·toneLPCCorr(SB), NOSPLIT, $0-60
	MOVD  x_base+0(FP), R0
	MOVD  cnt+24(FP), R1
	MOVD  delay+32(FP), R2
	MOVD  delay2+40(FP), R3

	VEOR V16.B16, V16.B16, V16.B16
	VEOR V17.B16, V17.B16, V17.B16
	VEOR V18.B16, V18.B16, V18.B16

	CMP   $1, R1
	BLT   reduce

	LSL   $2, R2, R2
	LSL   $2, R3, R3
	ADD   R0, R2, R5
	ADD   R0, R3, R6

	LSR   $2, R1, R7
	CBZ   R7, reduce

loop4:
	VLD1.P 16(R0), [V0.S4]
	VLD1.P 16(R5), [V1.S4]
	VLD1.P 16(R6), [V2.S4]
	VFMLA V0.S4, V0.S4, V16.S4
	VFMLA V0.S4, V1.S4, V17.S4
	VFMLA V0.S4, V2.S4, V18.S4
	SUBS  $1, R7, R7
	BNE   loop4

reduce:
	WORD  $0x6E30D600
	WORD  $0x6E20D400
	FMOVS F0, F3

	WORD  $0x6E31D620
	WORD  $0x6E20D400
	FMOVS F0, F4

	WORD  $0x6E32D640
	WORD  $0x6E20D400
	FMOVS F0, F5

	AND   $3, R1, R7
	CBZ   R7, store

tail:
	FMOVS (R0), F0
	FMOVS (R5), F1
	FMOVS (R6), F2
	FMADDS F0, F3, F0, F3
	FMADDS F1, F4, F0, F4
	FMADDS F2, F5, F0, F5
	ADD   $4, R0
	ADD   $4, R5
	ADD   $4, R6
	SUBS  $1, R7, R7
	BNE   tail

store:
	FMOVS F3, r00+48(FP)
	FMOVS F4, r01+52(FP)
	FMOVS F5, r02+56(FP)
	RET

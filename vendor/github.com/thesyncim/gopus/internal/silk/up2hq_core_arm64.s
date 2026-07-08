//go:build (arm64 && !race) && !purego

#include "textflag.h"

// func up2HQCore(out []int16, in []int16, sIIR *[6]int32)
TEXT ·up2HQCore(SB), NOSPLIT, $0-56
	MOVD out_base+0(FP), R0
	MOVD in_base+24(FP), R1
	MOVD in_len+32(FP), R2
	MOVD sIIR+48(FP), R3

	CBZ R2, up2hq_done

	MOVW (R3), R4
	MOVW 4(R3), R5
	MOVW 8(R3), R6
	MOVW 12(R3), R7
	MOVW 16(R3), R8
	MOVW 20(R3), R9

	MOVD $1746, R10
	MOVD $14986, R11
	MOVD $-26453, R12
	MOVD $6854, R13
	MOVD $25769, R14
	MOVD $-9994, R15
	MOVD $65535, R26

up2hq_loop:
	MOVH.P 2(R1), R16
	LSL    $10, R16, R16

	// Even all-pass branch.
	SUB  R4, R16, R17
	SXTW R17, R17
	MUL  R17, R10, R19
	ASR  $16, R19, R19
	ADD  R19, R4, R20
	ADD  R19, R16, R4

	SUB  R5, R20, R17
	SXTW R17, R17
	MUL  R17, R11, R19
	ASR  $16, R19, R19
	ADD  R19, R5, R21
	ADD  R19, R20, R5

	SUB  R6, R21, R17
	SXTW R17, R17
	MUL  R17, R12, R19
	ASR  $16, R19, R19
	ADD  R17, R19, R19
	ADD  R19, R6, R20
	ADD  R19, R21, R6

	ADD  $512, R20, R24
	ASR  $10, R24, R24
	ADD  $32768, R24, R25
	CMPW R26, R25
	BLS  up2hq_even_store
	TBZ  $31, R24, up2hq_even_pos
	MOVD $-32768, R24
	B    up2hq_even_store
up2hq_even_pos:
	MOVD $32767, R24
up2hq_even_store:
	MOVH.P R24, 2(R0)

	// Odd all-pass branch.
	SUB  R7, R16, R17
	SXTW R17, R17
	MUL  R17, R13, R19
	ASR  $16, R19, R19
	ADD  R19, R7, R20
	ADD  R19, R16, R7

	SUB  R8, R20, R17
	SXTW R17, R17
	MUL  R17, R14, R19
	ASR  $16, R19, R19
	ADD  R19, R8, R21
	ADD  R19, R20, R8

	SUB  R9, R21, R17
	SXTW R17, R17
	MUL  R17, R15, R19
	ASR  $16, R19, R19
	ADD  R17, R19, R19
	ADD  R19, R9, R20
	ADD  R19, R21, R9

	ADD  $512, R20, R24
	ASR  $10, R24, R24
	ADD  $32768, R24, R25
	CMPW R26, R25
	BLS  up2hq_odd_store
	TBZ  $31, R24, up2hq_odd_pos
	MOVD $-32768, R24
	B    up2hq_odd_store
up2hq_odd_pos:
	MOVD $32767, R24
up2hq_odd_store:
	MOVH.P R24, 2(R0)

	SUBS $1, R2, R2
	BNE  up2hq_loop

	MOVW R4, (R3)
	MOVW R5, 4(R3)
	MOVW R6, 8(R3)
	MOVW R7, 12(R3)
	MOVW R8, 16(R3)
	MOVW R9, 20(R3)

up2hq_done:
	RET

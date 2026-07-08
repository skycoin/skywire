//go:build (arm64) && !purego

#include "textflag.h"

// func cwrsiFastCore(n, k int, i uint32, y []int) uint32
//
// Mirrors libopus celt/cwrs.c:cwrsi for table-covered (n,k), using the fixed
// dense U table directly. The Go wrapper validates n/k/y before entering here.
TEXT ·cwrsiFastCore(SB), NOSPLIT, $0-52
	MOVD  n+0(FP), R1
	MOVD  k+8(FP), R2
	MOVWU i+16(FP), R3
	MOVD  y_base+24(FP), R7

	MOVD  $·pvqUDense(SB), R5
	MOVD  $708, R6
	MOVD  ZR, R4

	B     cwrs_loop_check

cwrs_loop_next:
	SUB $1, R1, R1

cwrs_loop_check:
	CMP $2, R1
	BLE cwrs_tail_n2

	CMP R1, R2
	BLT cwrs_dims

	// Lots of pulses: row = U[nCur].
	MADD  R6, R5, R1, R8
	ADD   $1, R2, R9
	MOVWU (R8)(R9<<2), R10

	// s = -(i >= p); i -= p&s
	SUB   R10, R3, R11
	CMPW  R10, R3
	CSETM HS, R12
	CSEL  HS, R11, R3, R3

	MOVD  R2, R13
	MOVWU (R8)(R1<<2), R10
	CMPW  R10, R3
	BCS   cwrs_pulses_linear

	// q > i: walk rows downward at fixed column nCur.
	MOVD R1, R2
cwrs_pulses_rows:
	SUB   $1, R2, R2
	MADD  R6, R5, R2, R8
	MOVWU (R8)(R1<<2), R10
	CMPW  R10, R3
	BCC   cwrs_pulses_rows
	B     cwrs_pulses_store

cwrs_pulses_linear:
	// Otherwise walk the current row downward. This matches libopus; in the
	// common case it exits after the first load.
	MADD  R6, R5, R1, R8
cwrs_pulses_linear_loop:
	MOVWU (R8)(R2<<2), R10
	CMPW  R10, R3
	BCS   cwrs_pulses_store
	SUB   $1, R2, R2
	B     cwrs_pulses_linear_loop

cwrs_pulses_store:
	SUB R10, R3, R3
	SUB R2, R13, R14
	ADD R12, R14, R14
	EOR R12, R14, R14
	MOVD R14, (R7)
	ADD  $8, R7, R7
	MADD R14, R4, R14, R4
	B    cwrs_loop_next

cwrs_dims:
	// Lots of dimensions: compare U[k][nCur] and U[k+1][nCur].
	MADD  R6, R5, R2, R8
	MOVWU (R8)(R1<<2), R10
	ADD   $1, R2, R9
	MADD  R6, R5, R9, R8
	MOVWU (R8)(R1<<2), R11

	CMPW R10, R3
	BCC  cwrs_dims_nonzero
	CMPW R11, R3
	BCS  cwrs_dims_nonzero

	SUB  R10, R3, R3
	MOVD ZR, (R7)
	ADD  $8, R7, R7
	B    cwrs_loop_next

cwrs_dims_nonzero:
	SUB   R11, R3, R8
	CMPW  R11, R3
	CSETM HS, R12
	CSEL  HS, R8, R3, R3

	MOVD R2, R13
cwrs_dims_rows:
	SUB   $1, R2, R2
	MADD  R6, R5, R2, R8
	MOVWU (R8)(R1<<2), R10
	CMPW  R10, R3
	BCC   cwrs_dims_rows

	SUB R10, R3, R3
	SUB R2, R13, R14
	ADD R12, R14, R14
	EOR R12, R14, R14
	MOVD R14, (R7)
	ADD  $8, R7, R7
	MADD R14, R4, R14, R4
	B    cwrs_loop_next

cwrs_tail_n2:
	ADD  R2, R2, R8
	ADD  $1, R8, R8
	SUB  R8, R3, R9
	CMPW R8, R3
	CSETM HS, R12
	CSEL HS, R9, R3, R3

	MOVD R2, R13
	ADD  $1, R3, R2
	LSR  $1, R2, R2
	CBZ  R2, cwrs_tail_n2_store
	ADD  R2, R2, R8
	SUB  $1, R8, R8
	SUB  R8, R3, R3

cwrs_tail_n2_store:
	SUB R2, R13, R14
	ADD R12, R14, R14
	EOR R12, R14, R14
	MOVD R14, (R7)
	ADD  $8, R7, R7
	MADD R14, R4, R14, R4

	// n == 1
	NEG  R3, R12
	ADD  R12, R2, R14
	EOR  R12, R14, R14
	MOVD R14, (R7)
	MADD R14, R4, R14, R4

	MOVW R4, ret+48(FP)
	RET

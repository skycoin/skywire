//go:build arm64 && !purego

#include "textflag.h"

// func celtInnerProd8FMA32(x, y []float32, n int) float32
//
// Computes the float32 inner product of x[0..n-1] and y[0..n-1] using the same
// 4-lane fused-multiply-add accumulation order as celtInnerProdNeonStyle in
// bands_quant.go, which mirrors libopus arm/pitch_neon_intr.c
// celt_inner_prod_neon (vfmaq_f32 lanes). The lane layout and horizontal
// reduction order are bit-identical to the Go reference:
//
//	main loop (step 8): acc[0..3] += x[i..i+3]*y[i..i+3]
//	                    acc[0..3] += x[i+4..i+7]*y[i+4..i+7]
//	4-tail:             acc[0..3] += x[i..i+3]*y[i..i+3]
//	reduce:             sum = (acc[0]+acc[2]) + (acc[1]+acc[3])
//	scalar tail:        sum = fma(x[i], y[i], sum)
//
// Register allocation:
//   R0 = x base pointer (advanced by VLD1.P / scalar tail)
//   R1 = y base pointer (advanced by VLD1.P / scalar tail)
//   R2 = n
//   R3 = i (element index into the 8/4-wide body)
//   R4 = main-loop limit (n-7); R5 = remaining-element count
//   V16 = 4-lane FMA accumulator
//   V0/V1 = loaded x/y lanes
//   F2..F5 = reduction temporaries, F6 = running sum
TEXT ·celtInnerProd8FMA32(SB), NOSPLIT, $0-60
	MOVD x_base+0(FP), R0
	MOVD y_base+24(FP), R1
	MOVD n+48(FP), R2

	// acc = {0,0,0,0}
	VEOR V16.B16, V16.B16, V16.B16

	MOVD ZR, R3                   // i = 0

	// Main loop: while i < n-7, process 8 elements (two 4-wide FMLA).
	SUB  $7, R2, R4               // R4 = n-7
	CMP  R4, R3
	BGE  ip_tail4_check

ip_loop8:
	VLD1.P 16(R0), [V0.S4]        // x[i..i+3]
	VLD1.P 16(R1), [V1.S4]        // y[i..i+3]
	VFMLA  V0.S4, V1.S4, V16.S4   // acc += x*y (lanes 0..3)

	VLD1.P 16(R0), [V0.S4]        // x[i+4..i+7]
	VLD1.P 16(R1), [V1.S4]        // y[i+4..i+7]
	VFMLA  V0.S4, V1.S4, V16.S4   // acc += x*y (lanes 4..7 -> same accumulator)

	ADD  $8, R3
	CMP  R4, R3
	BLT  ip_loop8

ip_tail4_check:
	// if n-i >= 4, process one more 4-wide group into acc.
	SUB  R3, R2, R5               // R5 = n - i (remaining)
	CMP  $4, R5
	BLT  ip_reduce

	VLD1.P 16(R0), [V0.S4]
	VLD1.P 16(R1), [V1.S4]
	VFMLA  V0.S4, V1.S4, V16.S4
	ADD    $4, R3

ip_reduce:
	// sum = (acc[0]+acc[2]) + (acc[1]+acc[3])
	// Extract each lane into lane 0 of a scratch vector; lane 0 aliases the
	// equivalently-numbered F register, so FADDS then operates on scalars.
	VMOV  V16.S[0], V2.S[0]
	VMOV  V16.S[2], V3.S[0]
	FADDS F2, F3, F2              // acc[0]+acc[2]
	VMOV  V16.S[1], V4.S[0]
	VMOV  V16.S[3], V5.S[0]
	FADDS F4, F5, F4             // acc[1]+acc[3]
	FADDS F2, F4, F6             // sum

	// scalar tail: for ; i < n; i++ { sum = fma(x[i], y[i], sum) }
ip_tail1:
	CMP  R2, R3
	BGE  ip_done
	FMOVS  (R0), F0
	FMOVS  (R1), F1
	FMADDS F1, F6, F0, F6        // F6 = F0*F1 + F6 = x*y + sum
	ADD    $4, R0
	ADD    $4, R1
	ADD    $1, R3
	B      ip_tail1

ip_done:
	FMOVS F6, ret+56(FP)
	RET

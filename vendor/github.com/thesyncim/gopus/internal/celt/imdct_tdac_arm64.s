//go:build arm64 && !purego

#include "textflag.h"

// func imdctTDACWindowFMA32(out, xsrc, window []float32, yOut0, xOut0, xSrc0, wBwd0, count int)
//
// For each step i in [0, count):
//   x1 = xsrc[xSrc0-i]
//   x2 = out[yOut0+i]
//   w1 = window[i]
//   w2 = window[wBwd0-i]
//   out[yOut0+i] = round(x2*w2 + round(-(x1*w1)))
//   out[xOut0-i] = round(x2*w1 + round( x1*w2))
//
// The vector loop computes four steps per iteration (reversed contiguous loads
// for the descending streams, identical per-lane FMUL/FNEG/FMLA sequence,
// reversed contiguous backward store). It only runs when the forward write
// range cannot touch the backward write/read ranges within the call — checked
// in the prologue — because the scalar order interleaves the two ends; the
// scalar loop handles the gated-out shapes and the count%4 tail.
TEXT ·imdctTDACWindowFMA32(SB), NOSPLIT, $0-112
	MOVD out_base+0(FP), R0
	MOVD xsrc_base+24(FP), R1
	MOVD window_base+48(FP), R2
	MOVD yOut0+72(FP), R4
	MOVD xOut0+80(FP), R5
	MOVD xSrc0+88(FP), R6
	MOVD wBwd0+96(FP), R7
	MOVD count+104(FP), R3

	CBZ  R3, tdac_done

	// yptr = out + 4*yOut0 (forward)
	LSL  $2, R4, R14
	ADD  R0, R14, R8

	// xOutPtr = out + 4*xOut0 (backward)
	LSL  $2, R5, R14
	ADD  R0, R14, R9

	// xSrcPtr = xsrc + 4*xSrc0 (backward)
	LSL  $2, R6, R14
	ADD  R1, R14, R10

	// w1ptr = window (forward), w2ptr = window + 4*wBwd0 (backward)
	MOVD R2, R11
	LSL  $2, R7, R14
	ADD  R2, R14, R12

	// Vector gate: blocks = count>>2; need the forward writes
	// [yOut0, yOut0+count) disjoint from the backward writes
	// [xOut0-count+1, xOut0] and, when out and xsrc share a base, from the
	// backward reads [xSrc0-count+1, xSrc0].
	LSR  $2, R3, R13
	CBZ  R13, tdac_scalar
	SUB  $1, R3, R14
	ADD  R4, R14, R15 // yOut0+count-1
	SUB  R14, R5, R16 // xOut0-count+1
	CMP  R15, R16
	BLE  tdac_scalar
	CMP  R0, R1
	BNE  tdac_vec
	SUB  R14, R6, R16 // xSrc0-count+1
	CMP  R15, R16
	BLE  tdac_scalar

tdac_vec:
	AND  $3, R3, R3 // scalar tail count

	// Descending block bases point at the lowest element of each 4-group.
	SUB  $12, R10, R10
	SUB  $12, R12, R12
	SUB  $12, R9, R9

tdac_vec_loop:
	VLD1 (R10), [V0.S4] // x1 ascending memory
	SUB  $16, R10
	VREV64 V0.S4, V0.S4
	VEXT $8, V0.B16, V0.B16, V0.B16
	VLD1 (R8), [V1.S4]   // x2
	VLD1.P 16(R11), [V2.S4] // w1
	VLD1 (R12), [V3.S4] // w2 ascending memory
	SUB  $16, R12
	VREV64 V3.S4, V3.S4
	VEXT $8, V3.B16, V3.B16, V3.B16

	WORD $0x6E22DC06          // FMUL V6.4S, V0.4S, V2.4S (x1*w1, rounded)
	WORD $0x6EA0F8C6          // FNEG V6.4S, V6.4S
	VFMLA V3.S4, V1.S4, V6.S4 // fwd = -(x1*w1) + x2*w2
	WORD $0x6E23DC07          // FMUL V7.4S, V0.4S, V3.4S (x1*w2, rounded)
	VFMLA V2.S4, V1.S4, V7.S4 // bwd = round(x1*w2) + x2*w1

	VST1.P [V6.S4], 16(R8)
	VREV64 V7.S4, V7.S4
	VEXT $8, V7.B16, V7.B16, V7.B16
	VST1 [V7.S4], (R9)
	SUB  $16, R9

	SUBS $1, R13
	BNE  tdac_vec_loop

	// Rebase the descending pointers for the scalar tail.
	ADD  $12, R10, R10
	ADD  $12, R12, R12
	ADD  $12, R9, R9

tdac_scalar:
	CBZ  R3, tdac_done

tdac_tail:
	FMOVS (R10), F0
	FMOVS (R8), F1
	FMOVS (R11), F2
	FMOVS (R12), F3

	FMULS  F2, F0, F4
	FNEGS  F4, F4
	FMADDS F3, F4, F1, F4

	FMULS  F3, F0, F5
	FMADDS F2, F5, F1, F5

	FMOVS F4, (R8)
	FMOVS F5, (R9)

	ADD  $4, R8, R8
	SUB  $4, R9, R9
	SUB  $4, R10, R10
	ADD  $4, R11, R11
	SUB  $4, R12, R12

	SUBS $1, R3, R3
	BNE  tdac_tail

tdac_done:
	RET

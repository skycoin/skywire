//go:build arm64 && !purego

#include "textflag.h"

// func expRotation1PassNeon(x []float32, first, stride, blocks, dir int, c, s float32)
//
// Runs blocks*4 steps of one expRotation1Norm pass starting at index `first`,
// advancing 4 indices per iteration in direction dir (+1 ascending for the
// forward pass, -1 descending for the backward pass; `first` is always the
// lowest index of the first block). Per index i:
//
//	x1 = x[i]; x2 = x[i+stride]
//	x[i+stride] = c*x2 + round(s*x1)
//	x[i]        = c*x1 + round(-s*x2)
//
// Each lane runs the exact scalar op sequence (rounded FMUL, fused FMLA), so
// results are bit-identical; the caller guarantees stride >= 4 so the four
// lanes of a block never touch each other's indices and the cross-iteration
// dependency (distance stride) is honored by the block order.
TEXT ·expRotation1PassNeon(SB), NOSPLIT, $0-64
	MOVD  x_base+0(FP), R0
	MOVD  first+24(FP), R1
	MOVD  stride+32(FP), R2
	MOVD  blocks+40(FP), R3
	MOVD  dir+48(FP), R4
	FMOVS c+56(FP), F8
	FMOVS s+60(FP), F9

	CBZ   R3, rot_done

	VDUP  V8.S[0], V8.S4 // c lanes
	VDUP  V9.S[0], V9.S4 // s lanes
	WORD  $0x6EA0F92A    // FNEG V10.4S, V9.4S (-s lanes)

	ADD   R1<<2, R0, R5  // &x[first]
	ADD   R2<<2, R5, R6  // &x[first+stride]
	LSL   $4, R4, R4     // byte step per block (+16 or -16)

rot_loop:
	VLD1 (R5), [V0.S4] // x1 lanes
	VLD1 (R6), [V1.S4] // x2 lanes

	WORD $0x6E29DC02          // FMUL V2.4S, V0.4S, V9.4S (s*x1, rounded)
	VFMLA V8.S4, V1.S4, V2.S4 // x2' = round(s*x1) + c*x2
	WORD $0x6E2ADC23          // FMUL V3.4S, V1.4S, V10.4S (-s*x2, rounded)
	VFMLA V8.S4, V0.S4, V3.S4 // x1' = round(-s*x2) + c*x1

	VST1 [V2.S4], (R6)
	VST1 [V3.S4], (R5)

	ADD  R4, R5, R5
	ADD  R4, R6, R6
	SUBS $1, R3
	BNE  rot_loop

rot_done:
	RET

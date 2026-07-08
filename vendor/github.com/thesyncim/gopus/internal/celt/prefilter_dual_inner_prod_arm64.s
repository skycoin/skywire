//go:build arm64 && !purego

#include "textflag.h"

// func prefilterDualInnerProdAsm(x, y1, y2 []float32, length int) (float32, float32)
//
// Computes two float32 inner products simultaneously, sum1 = <x,y1> and
// sum2 = <x,y2>, using the same 4-lane fused-multiply-add accumulation order as
// the prefilterDualInnerProdF32NeonOrder Go reference (libopus
// arm/pitch_neon_intr.c dual_inner_prod_neon). The lane layout, the
// (acc0+acc2)+(acc1+acc3) reductions, and the FMA tail are bit-identical to the
// Go path; the scalar tail multiply-add fuses on arm64, so it uses FMADDS too.
//
// Register allocation:
//   R0 = x base, R1 = y1 base, R2 = y2 base
//   R3 = length
//   R4 = i (element index)
//   R5 = main-loop limit (length-7) / remaining
//   V16 = acc1 (x*y1), V17 = acc2 (x*y2)
//   V0 = x lanes, V1 = y1 lanes, V2 = y2 lanes
//   F3..F8 = reduction / tail temporaries
TEXT ·prefilterDualInnerProdAsm(SB), NOSPLIT, $0-88
	MOVD x_base+0(FP), R0
	MOVD y1_base+24(FP), R1
	MOVD y2_base+48(FP), R2
	MOVD length+72(FP), R3

	VEOR V16.B16, V16.B16, V16.B16   // acc1 = 0
	VEOR V17.B16, V17.B16, V17.B16   // acc2 = 0

	MOVD ZR, R4                      // i = 0

	SUB  $7, R3, R5                  // limit = length-7
	CMP  R5, R4
	BGE  dip_tail4

dip_loop8:
	// lanes i..i+3
	VLD1 (R0), [V0.S4]
	VLD1 (R1), [V1.S4]
	VLD1 (R2), [V2.S4]
	VFMLA V0.S4, V1.S4, V16.S4       // acc1 += x*y1
	VFMLA V0.S4, V2.S4, V17.S4       // acc2 += x*y2

	// lanes i+4..i+7
	ADD  $16, R0
	ADD  $16, R1
	ADD  $16, R2
	VLD1 (R0), [V0.S4]
	VLD1 (R1), [V1.S4]
	VLD1 (R2), [V2.S4]
	VFMLA V0.S4, V1.S4, V16.S4
	VFMLA V0.S4, V2.S4, V17.S4
	ADD  $16, R0
	ADD  $16, R1
	ADD  $16, R2

	ADD  $8, R4
	CMP  R5, R4
	BLT  dip_loop8

dip_tail4:
	SUB  R4, R3, R6                  // remaining = length - i
	CMP  $4, R6
	BLT  dip_reduce

	VLD1 (R0), [V0.S4]
	VLD1 (R1), [V1.S4]
	VLD1 (R2), [V2.S4]
	VFMLA V0.S4, V1.S4, V16.S4
	VFMLA V0.S4, V2.S4, V17.S4
	ADD  $16, R0
	ADD  $16, R1
	ADD  $16, R2
	ADD  $4, R4

dip_reduce:
	// sum1 = (acc1[0]+acc1[2]) + (acc1[1]+acc1[3])
	VMOV  V16.S[0], V3.S[0]
	VMOV  V16.S[2], V4.S[0]
	FADDS F3, F4, F3
	VMOV  V16.S[1], V5.S[0]
	VMOV  V16.S[3], V6.S[0]
	FADDS F5, F6, F5
	FADDS F3, F5, F7                 // F7 = sum1

	// sum2 = (acc2[0]+acc2[2]) + (acc2[1]+acc2[3])
	VMOV  V17.S[0], V3.S[0]
	VMOV  V17.S[2], V4.S[0]
	FADDS F3, F4, F3
	VMOV  V17.S[1], V5.S[0]
	VMOV  V17.S[3], V6.S[0]
	FADDS F5, F6, F5
	FADDS F3, F5, F8                 // F8 = sum2

	// scalar tail: for ; i < length; i++ { sum1 += x*y1; sum2 += x*y2 }
dip_tail1:
	CMP  R3, R4
	BGE  dip_done
	FMOVS  (R0), F0
	FMOVS  (R1), F1
	FMOVS  (R2), F2
	FMADDS F1, F7, F0, F7            // sum1 += x*y1
	FMADDS F2, F8, F0, F8            // sum2 += x*y2
	ADD    $4, R0
	ADD    $4, R1
	ADD    $4, R2
	ADD    $1, R4
	B      dip_tail1

dip_done:
	FMOVS F7, ret+80(FP)
	FMOVS F8, ret1+84(FP)
	RET

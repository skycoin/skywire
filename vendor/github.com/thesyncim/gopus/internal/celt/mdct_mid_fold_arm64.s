//go:build arm64 && !purego

#include "textflag.h"

// func mdctMidFoldStoreNeon(dst []kissCpx, bitrev []int, samples []float32, trig []float32, i0, n4, xp1, xp2, blocks int, preScale float32)
//
// Vectorized body of the forward-MDCT middle fold (the no-window region) on
// the mdctUseFMALikeMixEnabled path. Per output i:
//
//	re = samples[xp2-2k], im = samples[xp1+2k]
//	yr = fma(re, t0, -(im*t1)); yi = fma(im, t0, re*t1)
//	dst[bitrev[i]] = kissCpx{yr*preScale, yi*preScale}
//
// Each lane computes exactly the scalar op sequence of
// mdctStoreDirectStageFMALike (rounded FMUL, FNEG, fused FMLA, scaling FMUL),
// so the result is bit-identical per element; only the bit-reversed store is
// scalar. Processes 4 outputs per iteration for `blocks` iterations; the Go
// caller handles the remainder.
TEXT ·mdctMidFoldStoreNeon(SB), NOSPLIT, $0-140
	MOVD  dst_base+0(FP), R0
	MOVD  bitrev_base+24(FP), R1
	MOVD  samples_base+48(FP), R2
	MOVD  trig_base+72(FP), R4
	MOVD  i0+96(FP), R10
	MOVD  n4+104(FP), R11
	MOVD  xp1+112(FP), R12
	MOVD  xp2+120(FP), R13
	MOVD  blocks+128(FP), R6
	FMOVS preScale+136(FP), F31
	VDUP  V31.S[0], V31.S4

	// R1 = &bitrev[i0]; R2 = &samples[xp1]; R3 = &samples[xp2-6];
	// R4 = &trig[i0];   R5 = &trig[n4+i0]
	ADD R10<<3, R1, R1
	ADD R12<<2, R2, R2
	SUB $6, R13, R13
	ADD R13<<2, R2, R3
	SUB R12<<2, R3, R3
	ADD R10<<2, R4, R4
	ADD R11<<2, R4, R5

	CBZ R6, done

loop:
	// im[0..3] = even lanes ascending from xp1.
	VLD2.P 32(R2), [V0.S4, V1.S4]

	// re[0..3] = even lanes from xp2-6, reversed to descend from xp2.
	VLD2 (R3), [V2.S4, V3.S4]
	SUB  $32, R3
	VREV64 V2.S4, V2.S4
	VEXT $8, V2.B16, V2.B16, V2.B16

	VLD1.P 16(R4), [V4.S4] // t0
	VLD1.P 16(R5), [V5.S4] // t1

	WORD $0x6E25DC06 // FMUL V6.4S, V0.4S, V5.4S   (im*t1, rounded)
	WORD $0x6EA0F8C6 // FNEG V6.4S, V6.4S
	VFMLA V4.S4, V2.S4, V6.S4 // yr = -(im*t1) + re*t0 (fused)
	WORD $0x6E25DC47 // FMUL V7.4S, V2.4S, V5.4S   (re*t1, rounded)
	VFMLA V4.S4, V0.S4, V7.S4 // yi = re*t1 + im*t0 (fused)
	WORD $0x6E3FDCC6 // FMUL V6.4S, V6.4S, V31.4S  (yr*preScale)
	WORD $0x6E3FDCE7 // FMUL V7.4S, V7.4S, V31.4S  (yi*preScale)

	// Interleave to (r,i) pairs and scatter through bitrev.
	VZIP1 V7.S4, V6.S4, V8.S4
	VZIP2 V7.S4, V6.S4, V9.S4

	MOVD.P 8(R1), R7
	ADD    R7<<3, R0, R8
	VMOV   V8.D[0], R9
	MOVD   R9, (R8)
	MOVD.P 8(R1), R7
	ADD    R7<<3, R0, R8
	VMOV   V8.D[1], R9
	MOVD   R9, (R8)
	MOVD.P 8(R1), R7
	ADD    R7<<3, R0, R8
	VMOV   V9.D[0], R9
	MOVD   R9, (R8)
	MOVD.P 8(R1), R7
	ADD    R7<<3, R0, R8
	VMOV   V9.D[1], R9
	MOVD   R9, (R8)

	SUBS $1, R6
	BNE  loop

done:
	RET

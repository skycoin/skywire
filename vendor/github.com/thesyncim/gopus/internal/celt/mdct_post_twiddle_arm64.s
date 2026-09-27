//go:build arm64 && !purego

#include "textflag.h"

// func mdctPostTwiddleNeon(coeffs []float32, fftStage []kissCpx, trig []float32, n2, n4, pairBlocks int)
//
// Forward-MDCT post-twiddle for the non-QEXT scale placement. Per index i
// (lo = 2i ascending evens, hi = n2-1-2i descending odds):
//
//	yr = round(im*trig[n4+i]) - round(re*trig[i])
//	yi = round(re*trig[n4+i]) + round(im*trig[i])
//	coeffs[lo] = yr; coeffs[hi] = yi
//
// Each iteration processes a mirror pair of 4-wide blocks (i ascending and
// j = n4-1-i descending): the forward block's yr lanes and the reversed
// mirror block's yi lanes tile coeffs[2i..2i+7] contiguously, and vice versa
// at the top, so both ends store as zipped 32-byte writes. Lane ops are the
// exact scalar sequence (rounded FMUL, plain FADD/FSUB) — bit-identical per
// element. The Go caller finishes the n4%8 middle scalarly.
TEXT ·mdctPostTwiddleNeon(SB), NOSPLIT, $0-96
	MOVD coeffs_base+0(FP), R0
	MOVD fftStage_base+24(FP), R2
	MOVD trig_base+48(FP), R4
	MOVD n2+72(FP), R8
	MOVD n4+80(FP), R9
	MOVD pairBlocks+88(FP), R10

	CBZ  R10, ptw_done

	// High-end write pointer: &coeffs[n2-8].
	SUB  $8, R8, R11
	ADD  R11<<2, R0, R1
	// Mirror fftStage block base: &fftStage[n4-4] (8-byte complex).
	SUB  $4, R9, R11
	ADD  R11<<3, R2, R3
	// Mirror trig block bases: &trig[n4-4], &trig[2*n4-4].
	ADD  R11<<2, R4, R5
	ADD  R9<<2, R5, R7
	// Forward trigHi pointer: &trig[n4].
	ADD  R9<<2, R4, R6

ptw_loop:
	VLD2.P 32(R2), [V0.S4, V1.S4] // forward re/im
	VLD2 (R3), [V2.S4, V3.S4]     // mirror re/im (lanes ascending j)
	SUB  $32, R3
	VLD1.P 16(R4), [V4.S4] // t0 forward
	VLD1.P 16(R6), [V5.S4] // t1 forward
	VLD1 (R5), [V6.S4]     // t0 mirror
	SUB  $16, R5
	VLD1 (R7), [V7.S4]     // t1 mirror
	SUB  $16, R7

	WORD $0x6E25DC30 // FMUL V16, V1, V5 (imF*t1F, rounded)
	WORD $0x6E24DC11 // FMUL V17, V0, V4 (reF*t0F, rounded)
	WORD $0x4EB1D610 // FSUB V16, V16, V17 (yrF)
	WORD $0x6E25DC12 // FMUL V18, V0, V5 (reF*t1F, rounded)
	WORD $0x6E24DC33 // FMUL V19, V1, V4 (imF*t0F, rounded)
	WORD $0x4E33D652 // FADD V18, V18, V19 (yiF)
	WORD $0x6E27DC74 // FMUL V20, V3, V7 (imM*t1M, rounded)
	WORD $0x6E26DC55 // FMUL V21, V2, V6 (reM*t0M, rounded)
	WORD $0x4EB5D694 // FSUB V20, V20, V21 (yrM)
	WORD $0x6E27DC56 // FMUL V22, V2, V7 (reM*t1M, rounded)
	WORD $0x6E26DC77 // FMUL V23, V3, V6 (imM*t0M, rounded)
	WORD $0x4E37D6D6 // FADD V22, V22, V23 (yiM)

	// Low region: zip(yrF, reversed yiM) -> coeffs[2i..2i+7].
	VREV64 V22.S4, V22.S4
	VEXT $8, V22.B16, V22.B16, V22.B16
	VZIP1 V22.S4, V16.S4, V24.S4
	VZIP2 V22.S4, V16.S4, V25.S4
	VST1.P [V24.S4, V25.S4], 32(R0)

	// High region: zip(yrM, reversed yiF) -> coeffs[2j0..2j0+7].
	VREV64 V18.S4, V18.S4
	VEXT $8, V18.B16, V18.B16, V18.B16
	VZIP1 V18.S4, V20.S4, V26.S4
	VZIP2 V18.S4, V20.S4, V27.S4
	VST1 [V26.S4, V27.S4], (R1)
	SUB  $32, R1

	SUBS $1, R10
	BNE  ptw_loop

ptw_done:
	RET

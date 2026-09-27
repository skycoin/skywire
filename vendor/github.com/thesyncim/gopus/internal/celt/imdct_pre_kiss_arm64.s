//go:build arm64 && !purego

#include "textflag.h"

// func imdctPreRotateFMA32Kiss(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int)
//
// Reproduces the FMA-like IMDCT pre-rotation:
//   x1 = spectrum[2*i]
//   x2 = spectrum[n2-1-2*i]
//   t0 = trig[i]
//   t1 = trig[n4+i]
//   yr = round(x1*t0 + round(-(x2*t1)))
//   yi = round(x2*t0 + round(x1*t1))
//   fftIn[i] = complex(yr, yi)
//
// The main loop computes four outputs per iteration with NEON: x1 comes from
// the even lanes of a paired load, x2 from the reversed odd lanes of a paired
// load walking down from the top of the spectrum, and each lane runs exactly
// the scalar op sequence (rounded FMUL, FNEG, fused FMLA), so results are
// bit-identical per element. A scalar loop finishes n4%4.
TEXT ·imdctPreRotateFMA32Kiss(SB), NOSPLIT, $0-88
	MOVD fftIn_base+0(FP), R0
	MOVD spectrum_base+24(FP), R1
	MOVD trig_base+48(FP), R2
	MOVD n2+72(FP), R12
	MOVD n4+80(FP), R3

	CMP  $1, R3
	BLT  pre_kiss_done

	// Forward spectrum pointer (spectrum[2*i]).
	MOVD R1, R5

	// Forward trig pointers: t0 from trig[0], t1 from trig[n4].
	MOVD R2, R7
	ADD  R3<<2, R2, R8

	LSR  $2, R3, R9 // four-wide blocks
	AND  $3, R3, R3 // scalar tail count

	// Reverse x2 block base: &spectrum[n2-8] (odd lanes reversed give
	// spectrum[n2-1-2i .. n2-7-2i]).
	SUB  $8, R12, R6
	ADD  R6<<2, R1, R6

	CBZ  R9, pre_kiss_tail_setup

pre_kiss_vec:
	VLD2.P 32(R5), [V0.S4, V1.S4] // V0 = x1 lanes
	VLD2 (R6), [V2.S4, V3.S4]
	SUB  $32, R6
	VREV64 V3.S4, V3.S4
	VEXT $8, V3.B16, V3.B16, V3.B16 // V3 = x2 lanes (descending)
	VLD1.P 16(R7), [V4.S4]          // t0
	VLD1.P 16(R8), [V5.S4]          // t1

	WORD $0x6E25DC66           // FMUL V6.4S, V3.4S, V5.4S (x2*t1, rounded)
	WORD $0x6EA0F8C6           // FNEG V6.4S, V6.4S
	VFMLA V4.S4, V0.S4, V6.S4  // yr = -(x2*t1) + x1*t0
	WORD $0x6E25DC07           // FMUL V7.4S, V0.4S, V5.4S (x1*t1, rounded)
	VFMLA V4.S4, V3.S4, V7.S4  // yi = x1*t1 + x2*t0

	VZIP1 V7.S4, V6.S4, V8.S4
	VZIP2 V7.S4, V6.S4, V9.S4
	VST1.P [V8.S4, V9.S4], 32(R0)

	SUBS $1, R9
	BNE  pre_kiss_vec

pre_kiss_tail_setup:
	// Next scalar x2 element: the block base points 7 floats below it.
	ADD  $28, R6, R6

pre_kiss_tail:
	CBZ  R3, pre_kiss_done

	FMOVS (R5), F0
	FMOVS (R6), F1
	FMOVS (R7), F2
	FMOVS (R8), F3

	FMULS  F3, F1, F4
	FNEGS  F4, F4
	FMADDS F2, F4, F0, F4

	FMULS  F3, F0, F5
	FMADDS F2, F5, F1, F5

	FMOVS F4, (R0)
	FMOVS F5, 4(R0)

	ADD  $8, R5, R5
	SUB  $8, R6, R6
	ADD  $4, R7, R7
	ADD  $4, R8, R8
	ADD  $8, R0, R0

	SUBS $1, R3, R3
	BNE  pre_kiss_tail

pre_kiss_done:
	RET

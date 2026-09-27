//go:build (arm64) && !purego

#include "textflag.h"

// func imdctPostRotateF32FromKiss(buf []float32, fft []kissCpx, trig []float32, n2, n4 int)
//
// Equivalent to interleaving fft into buf and then calling imdctPostRotateF32,
// but reads kissCpx scratch directly. Per iteration i (k = n4-1-i):
//   re = fft[i].i, im = fft[i].r
//   buf[2i]        = fma(re, trig[i],      round(im*trig[n4+i]))
//   buf[n2-1-2i]   = fma(re, trig[n4+i], -round(im*trig[i]))
//   re2 = fft[k].i, im2 = fft[k].r
//   buf[n2-2-2i]   = fma(re2, trig[n4-1-i], round(im2*trig[n2-1-i]))
//   buf[2i+1]      = fma(re2, trig[n2-1-i], -round(im2*trig[n4-1-i]))
//
// The main loop computes four iterations at once with NEON (eight outputs per
// end), lane-for-lane running the identical scalar op sequence (rounded FMUL,
// FNEG, fused FMLA), so results are bit-identical per element; a scalar loop
// finishes limit%4.
TEXT ·imdctPostRotateF32FromKiss(SB), NOSPLIT, $0-88
	MOVD buf_base+0(FP), R0
	MOVD fft_base+24(FP), R14
	MOVD trig_base+48(FP), R1
	MOVD n2+72(FP), R2
	MOVD n4+80(FP), R3

	// limit = (n4+1) >> 1
	ADD  $1, R3, R4
	LSR  $1, R4, R4
	CMP  $1, R4
	BLT  post_kiss_done

	LSR  $2, R4, R19 // four-wide blocks
	AND  $3, R4, R11 // scalar tail count

	// Forward trig pointers: t0 = &trig[0], t1 = &trig[n4].
	MOVD R1, R7
	ADD  R3<<2, R1, R8

	// Backward trig block bases: t0b from &trig[n4-4], t1b from &trig[n2-4].
	SUB  $4, R3, R9
	ADD  R9<<2, R1, R9
	SUB  $4, R2, R10
	ADD  R10<<2, R1, R10

	// fft pointers: forward &fft[0], backward block base &fft[n4-4].
	MOVD R14, R15
	SUB  $4, R3, R16
	ADD  R16<<3, R14, R16

	// Output pointers: near &buf[0] ascending, far &buf[n2-8] descending
	// (each block stores the pairs at buf[n2-1-2i-...] as one 32-byte write).
	MOVD R0, R12
	SUB  $8, R2, R13
	ADD  R13<<2, R0, R13

	CBZ  R19, post_kiss_scalar_setup

post_kiss_vec:
	// Forward four: V0 = im lanes (fft .r), V1 = re lanes (fft .i).
	VLD2.P 32(R15), [V0.S4, V1.S4]
	VLD1.P 16(R7), [V4.S4] // t0
	VLD1.P 16(R8), [V5.S4] // t1

	WORD $0x6E25DC06          // FMUL V6.4S, V0.4S, V5.4S (im*t1, rounded)
	VFMLA V4.S4, V1.S4, V6.S4 // yr = round(im*t1) + re*t0
	WORD $0x6E24DC07          // FMUL V7.4S, V0.4S, V4.4S (im*t0, rounded)
	WORD $0x6EA0F8E7          // FNEG V7.4S, V7.4S
	VFMLA V5.S4, V1.S4, V7.S4 // yi = -round(im*t0) + re*t1

	// Backward four (descending k): V2 = im2 lanes, V3 = re2 lanes.
	VLD2 (R16), [V2.S4, V3.S4]
	SUB  $32, R16
	VREV64 V2.S4, V2.S4
	VEXT $8, V2.B16, V2.B16, V2.B16
	VREV64 V3.S4, V3.S4
	VEXT $8, V3.B16, V3.B16, V3.B16

	VLD1 (R9), [V16.S4] // t0b ascending memory
	SUB  $16, R9
	VREV64 V16.S4, V16.S4
	VEXT $8, V16.B16, V16.B16, V16.B16
	VLD1 (R10), [V17.S4] // t1b
	SUB  $16, R10
	VREV64 V17.S4, V17.S4
	VEXT $8, V17.B16, V17.B16, V17.B16

	WORD $0x6E31DC53            // FMUL V19.4S, V2.4S, V17.4S (im2*t1b, rounded)
	VFMLA V16.S4, V3.S4, V19.S4 // yr2 = round(im2*t1b) + re2*t0b
	WORD $0x6E30DC54            // FMUL V20.4S, V2.4S, V16.4S (im2*t0b, rounded)
	WORD $0x6EA0FA94            // FNEG V20.4S, V20.4S
	VFMLA V17.S4, V3.S4, V20.S4 // yi2 = -round(im2*t0b) + re2*t1b

	// Near pairs (buf[2i], buf[2i+1]) = (yr, yi2), ascending.
	VZIP1 V20.S4, V6.S4, V8.S4
	VZIP2 V20.S4, V6.S4, V9.S4
	VST1.P [V8.S4, V9.S4], 32(R12)

	// Far pairs (buf[n2-2-2i], buf[n2-1-2i]) = (yr2, yi), descending: reverse
	// lanes so one 32-byte store lands the four pairs in memory order.
	VREV64 V19.S4, V19.S4
	VEXT $8, V19.B16, V19.B16, V19.B16
	VREV64 V7.S4, V7.S4
	VEXT $8, V7.B16, V7.B16, V7.B16
	VZIP1 V7.S4, V19.S4, V8.S4
	VZIP2 V7.S4, V19.S4, V9.S4
	VST1 [V8.S4, V9.S4], (R13)
	SUB  $32, R13

	SUBS $1, R19
	BNE  post_kiss_vec

post_kiss_scalar_setup:
	CBZ  R11, post_kiss_done

	// Rebase the scalar pointers: the vector loop left the descending block
	// bases 3 elements below the next scalar element.
	ADD  $12, R9, R9
	ADD  $12, R10, R10
	ADD  $24, R16, R16
	ADD  $24, R13, R13

post_kiss_tail:
	FMOVS 4(R15), F0
	FMOVS (R15), F1
	FMOVS (R7), F2
	FMOVS (R8), F3

	FMULS F3, F1, F4
	FMADDS F2, F4, F0, F4

	FMULS F2, F1, F5
	FNEGS F5, F5
	FMADDS F3, F5, F0, F5

	FMOVS 4(R16), F6
	FMOVS (R16), F7

	FMOVS F4, (R12)
	FMOVS F5, 4(R13)

	FMOVS (R9), F2
	FMOVS (R10), F3

	FMULS F3, F7, F4
	FMADDS F2, F4, F6, F4

	FMULS F2, F7, F5
	FNEGS F5, F5
	FMADDS F3, F5, F6, F5

	FMOVS F4, (R13)
	FMOVS F5, 4(R12)

	ADD  $8, R12, R12
	SUB  $8, R13, R13
	ADD  $4, R7, R7
	ADD  $4, R8, R8
	SUB  $4, R9, R9
	SUB  $4, R10, R10
	ADD  $8, R15, R15
	SUB  $8, R16, R16

	SUBS $1, R11, R11
	BNE  post_kiss_tail

post_kiss_done:
	RET

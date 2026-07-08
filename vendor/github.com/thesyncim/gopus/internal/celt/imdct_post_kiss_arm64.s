//go:build (arm64) && !purego

#include "textflag.h"

// func imdctPostRotateF32FromKiss(buf []float32, fft []kissCpx, trig []float32, n2, n4 int)
//
// Equivalent to interleaving fft into buf and then calling imdctPostRotateF32,
// but reads kissCpx scratch directly:
//   re = fft[i].i, im = fft[i].r
//   yr = round(re*t0 + round(im*t1))
//   yi = round(re*t1 + round(-(im*t0)))
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

	// Output byte offsets.
	MOVD ZR, R5
	SUB  $2, R2, R6
	LSL  $2, R6, R6

	// Forward trig pointers.
	MOVD R1, R7
	LSL  $2, R3, R8
	ADD  R1, R8, R8

	// Backward trig pointers.
	SUB  $1, R3, R9
	LSL  $2, R9, R9
	ADD  R1, R9, R9
	SUB  $1, R2, R10
	LSL  $2, R10, R10
	ADD  R1, R10, R10

	// kissCpx forward/backward pointers.
	MOVD R14, R15
	SUB  $1, R3, R16
	LSL  $3, R16, R16
	ADD  R14, R16, R16

	MOVD R4, R11

	CMP  $2, R11
	BLT  post_kiss_tail_check

post_kiss_loop2:
	// --- Forward rotation from fft[i] ---
	ADD  R0, R5, R12
	FMOVS 4(R15), F0
	FMOVS (R15), F1
	FMOVS (R7), F2
	FMOVS (R8), F3

	FMULS F3, F1, F4
	FMADDS F2, F4, F0, F4

	FMULS F2, F1, F5
	FNEGS F5, F5
	FMADDS F3, F5, F0, F5

	// --- Backward rotation from fft[n4-1-i] ---
	ADD  R0, R6, R13
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

	ADD  $8, R5, R5
	SUB  $8, R6, R6
	ADD  $4, R7, R7
	ADD  $4, R8, R8
	SUB  $4, R9, R9
	SUB  $4, R10, R10
	ADD  $8, R15, R15
	SUB  $8, R16, R16

	// --- Unrolled i+1 ---
	ADD  R0, R5, R12
	FMOVS 4(R15), F0
	FMOVS (R15), F1
	FMOVS (R7), F2
	FMOVS (R8), F3

	FMULS F3, F1, F4
	FMADDS F2, F4, F0, F4

	FMULS F2, F1, F5
	FNEGS F5, F5
	FMADDS F3, F5, F0, F5

	ADD  R0, R6, R13
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

	ADD  $8, R5, R5
	SUB  $8, R6, R6
	ADD  $4, R7, R7
	ADD  $4, R8, R8
	SUB  $4, R9, R9
	SUB  $4, R10, R10
	ADD  $8, R15, R15
	SUB  $8, R16, R16

	SUBS $2, R11, R11
	CMP  $2, R11
	BGE  post_kiss_loop2

post_kiss_tail_check:
	CBZ  R11, post_kiss_done

	// Tail iteration for an odd limit.
	ADD  R0, R5, R12
	FMOVS 4(R15), F0
	FMOVS (R15), F1
	FMOVS (R7), F2
	FMOVS (R8), F3

	FMULS F3, F1, F4
	FMADDS F2, F4, F0, F4

	FMULS F2, F1, F5
	FNEGS F5, F5
	FMADDS F3, F5, F0, F5

	ADD  R0, R6, R13
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

post_kiss_done:
	RET

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
	LSL  $2, R4, R4
	ADD  R0, R4, R8

	// xOutPtr = out + 4*xOut0 (backward)
	LSL  $2, R5, R5
	ADD  R0, R5, R9

	// xSrcPtr = xsrc + 4*xSrc0 (backward)
	LSL  $2, R6, R6
	ADD  R1, R6, R10

	// w1ptr = window (forward), w2ptr = window + 4*wBwd0 (backward)
	MOVD R2, R11
	LSL  $2, R7, R7
	ADD  R2, R7, R12

	CMP  $2, R3
	BLT  tdac_tail

tdac_loop2:
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

	FMOVS -4(R10), F0
	FMOVS 4(R8), F1
	FMOVS 4(R11), F2
	FMOVS -4(R12), F3

	FMULS  F2, F0, F4
	FNEGS  F4, F4
	FMADDS F3, F4, F1, F4

	FMULS  F3, F0, F5
	FMADDS F2, F5, F1, F5

	FMOVS F4, 4(R8)
	FMOVS F5, -4(R9)

	ADD  $8, R8, R8
	SUB  $8, R9, R9
	SUB  $8, R10, R10
	ADD  $8, R11, R11
	SUB  $8, R12, R12

	SUBS $2, R3, R3
	CMP  $2, R3
	BGE  tdac_loop2

tdac_tail:
	CBZ  R3, tdac_done

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

tdac_done:
	RET

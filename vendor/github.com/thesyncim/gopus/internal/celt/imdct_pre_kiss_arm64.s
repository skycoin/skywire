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
TEXT ·imdctPreRotateFMA32Kiss(SB), NOSPLIT, $0-88
	MOVD fftIn_base+0(FP), R0
	MOVD spectrum_base+24(FP), R1
	MOVD trig_base+48(FP), R2
	MOVD n2+72(FP), R12
	MOVD n4+80(FP), R3

	CMP  $1, R3
	BLT  pre_kiss_done

	// Forward spectrum pointer (spectrum[2*i], +8 bytes per i).
	MOVD R1, R5

	// Reverse spectrum pointer (spectrum[n2-1-2*i], -8 bytes per i).
	SUB  $1, R12, R6
	LSL  $2, R6, R6
	ADD  R1, R6, R6

	// Forward trig pointers: t0 from trig[0] (+4), t1 from trig[n4] (+4).
	MOVD R2, R7
	LSL  $2, R3, R8
	ADD  R2, R8, R8

	CMP  $2, R3
	BLT  pre_kiss_tail

pre_kiss_loop2:
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

	FMOVS 8(R5), F0
	FMOVS -8(R6), F1
	FMOVS 4(R7), F2
	FMOVS 4(R8), F3

	FMULS  F3, F1, F4
	FNEGS  F4, F4
	FMADDS F2, F4, F0, F4

	FMULS  F3, F0, F5
	FMADDS F2, F5, F1, F5

	FMOVS F4, 8(R0)
	FMOVS F5, 12(R0)

	ADD  $16, R5, R5
	SUB  $16, R6, R6
	ADD  $8, R7, R7
	ADD  $8, R8, R8
	ADD  $16, R0, R0

	SUBS $2, R3, R3
	CMP  $2, R3
	BGE  pre_kiss_loop2

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

pre_kiss_done:
	RET

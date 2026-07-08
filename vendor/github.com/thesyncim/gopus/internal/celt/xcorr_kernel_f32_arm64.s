//go:build arm64 && !purego

#include "textflag.h"

// func xcorrKernel4Float32Neon(x, y []float32, sum *[4]float32, length int)
//
// Computes sum[k] += sum_{i=0}^{length-1} x[i] * y[i+k] for k = 0..3, the
// 4-lag cross-correlation kernel of celt_pitch_xcorr. One FMLA per input
// sample accumulates all four lags at once: acc += dup(x[i]) * y[i:i+4].
// The fused multiply-add carries a single rounding (FMADDS semantics),
// matching the arm64 NEON reference order rather than the scalar
// multiply-then-add path. y must expose length+3 readable elements.
TEXT ·xcorrKernel4Float32Neon(SB), NOSPLIT, $0-64
	MOVD x_base+0(FP), R0
	MOVD y_base+24(FP), R1
	MOVD sum+48(FP), R2
	MOVD length+56(FP), R6

	// acc <- existing sum[0..3]
	VLD1 (R2), [V0.S4]

	CMP $0, R6
	BLE store

loop:
	FMOVS (R0), F4
	VDUP  V4.S[0], V5.S4
	VLD1  (R1), [V2.S4]
	VFMLA V5.S4, V2.S4, V0.S4

	ADD $4, R0
	ADD $4, R1
	SUB $1, R6
	CBNZ R6, loop

store:
	VST1 [V0.S4], (R2)
	RET

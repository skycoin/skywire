//go:build (amd64) && !purego

#include "textflag.h"

// func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32)
//
// Computes three float32 correlations for toneLPC using scalar VFMADD231SS
// in exact sequential accumulation order to match Go compiler output.
// Eliminates bounds checks for speed.
//
// Register allocation:
//   AX    = x base pointer
//   CX    = cnt (loop counter)
//   DX    = delay byte offset
//   SI    = delay2 byte offset
//   X0    = r00 accumulator
//   X1    = r01 accumulator
//   X2    = r02 accumulator
//   X3    = x[i]
//   X4    = x[i+delay]
//   X5    = x[i+delay2]
TEXT ·toneLPCCorrAVXFMA(SB), NOSPLIT, $0-60
	MOVQ x_base+0(FP), AX
	MOVQ cnt+24(FP), CX
	MOVQ delay+32(FP), DX
	MOVQ delay2+40(FP), SI

	// Byte offsets
	SHLQ $2, DX                   // delay * sizeof(float32)
	SHLQ $2, SI                   // delay2 * sizeof(float32)

	// Zero accumulators
	VXORPS X0, X0, X0            // r00 = 0
	VXORPS X1, X1, X1            // r01 = 0
	VXORPS X2, X2, X2            // r02 = 0

	TESTQ CX, CX
	JLE   store

loop:
	// Load x[i]
	VMOVSS (AX), X3

	// Load x[i+delay]
	VMOVSS (AX)(DX*1), X4

	// Load x[i+delay2]
	VMOVSS (AX)(SI*1), X5

	// r00 += x[i] * x[i]
	VFMADD231SS X3, X3, X0

	// r01 += x[i] * x[i+delay]
	VFMADD231SS X3, X4, X1

	// r02 += x[i] * x[i+delay2]
	VFMADD231SS X3, X5, X2

	ADDQ $4, AX                  // advance pointer
	DECQ CX
	JNZ  loop

store:
	VMOVSS X0, r00+48(FP)
	VMOVSS X1, r01+52(FP)
	VMOVSS X2, r02+56(FP)
	VZEROUPPER
	RET

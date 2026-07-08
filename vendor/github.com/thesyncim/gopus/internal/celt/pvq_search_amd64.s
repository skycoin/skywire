//go:build (amd64) && !purego

#include "textflag.h"

// func pvqSearchPulseLoop(absX, y []float32, iy []int, xy, yy float32, n, pulsesLeft int) (float32, float32)
//
// Places pulsesLeft pulses one at a time, merging the outer pulse loop and
// inner position search into a single assembly call.
//
// Stack frame layout (FP offsets):
//   absX:       0(FP)  = base+0, len+8, cap+16   (24 bytes)
//   y:          24(FP) = base+24, len+32, cap+40  (24 bytes)
//   iy:         48(FP) = base+48, len+56, cap+64  (24 bytes)
//   xy:         72(FP) (float32)
//   yy:         76(FP) (float32)
//   n:          80(FP) (int)
//   pulsesLeft: 88(FP) (int)
//   ret0 (xy):  96(FP) (float32)
//   ret1 (yy):  100(FP) (float32)
//
// Register allocation:
//   AX   = absX base
//   BX   = y base
//   R9   = iy base
//   CX   = n
//   R10  = pulsesLeft (outer counter)
//   DX   = j (inner counter)
//   R8   = bestID
//   R11  = temp
//   X14  = xy (float32, updated per pulse)
//   X15  = yy (float32, updated per pulse)
//   X12  = bestNum
//   X13  = bestDen
//   X8   = constant 1.0f
//   X9   = constant 2.0f
//   X0-X5 = temporaries
TEXT ·pvqSearchPulseLoopAVX(SB), NOSPLIT, $0-104
	MOVQ  absX_base+0(FP), AX
	MOVQ  y_base+24(FP), BX
	MOVQ  iy_base+48(FP), R9
	VMOVSS xy+72(FP), X14
	VMOVSS yy+76(FP), X15
	MOVQ  n+80(FP), CX
	MOVQ  pulsesLeft+88(FP), R10

	// Load constants
	MOVL  $0x3F800000, R11       // 1.0f
	MOVQ  R11, X8
	MOVL  $0x40000000, R11       // 2.0f
	MOVQ  R11, X9

	// If pulsesLeft <= 0 or n <= 0, return immediately
	TESTQ R10, R10
	JLE   ppl_done
	TESTQ CX, CX
	JLE   ppl_done

ppl_outer:
	// yy += 1
	VADDSS X8, X15, X15

	// Inner search: find bestID for this pulse
	// Init: position 0
	VMOVSS (AX), X0               // absX[0]
	VADDSS X14, X0, X0            // rxy = xy + absX[0]
	VMOVSS (BX), X1               // y[0]
	VADDSS X15, X1, X13           // bestDen = yy + y[0]
	VMULSS X0, X0, X12            // bestNum = rxy * rxy
	XORQ   R8, R8                 // bestID = 0

	CMPQ  CX, $1
	JLE   ppl_update

	MOVQ  $1, DX                  // j = 1

	// Check if we can do 2x unrolled loop
	LEAQ  -1(CX), R11
	CMPQ  DX, R11
	JGE   ppl_tail

ppl_inner2:
	// --- Iteration j ---
	VMOVSS (AX)(DX*4), X0
	VADDSS X14, X0, X0
	VMOVSS (BX)(DX*4), X1
	VADDSS X15, X1, X1
	VMULSS X0, X0, X2
	VMULSS X13, X2, X3
	VMULSS X1, X12, X4
	VUCOMISS X4, X3
	JBE   ppl_skip1
	MOVSS  X1, X13
	MOVSS  X2, X12
	MOVQ   DX, R8
ppl_skip1:

	// --- Iteration j+1 ---
	LEAQ  1(DX), SI
	VMOVSS (AX)(SI*4), X0
	VADDSS X14, X0, X0
	VMOVSS (BX)(SI*4), X1
	VADDSS X15, X1, X1
	VMULSS X0, X0, X2
	VMULSS X13, X2, X3
	VMULSS X1, X12, X4
	VUCOMISS X4, X3
	JBE   ppl_skip2
	MOVSS  X1, X13
	MOVSS  X2, X12
	MOVQ   SI, R8
ppl_skip2:

	ADDQ  $2, DX
	CMPQ  DX, R11
	JLT   ppl_inner2

ppl_tail:
	CMPQ  DX, CX
	JGE   ppl_update

	VMOVSS (AX)(DX*4), X0
	VADDSS X14, X0, X0
	VMOVSS (BX)(DX*4), X1
	VADDSS X15, X1, X1
	VMULSS X0, X0, X2
	VMULSS X13, X2, X3
	VMULSS X1, X12, X4
	VUCOMISS X4, X3
	JBE   ppl_update
	MOVSS  X1, X13
	MOVSS  X2, X12
	MOVQ   DX, R8

ppl_update:
	// xy += absX[bestID]
	VMOVSS (AX)(R8*4), X0
	VADDSS X0, X14, X14

	// yy += y[bestID]
	VMOVSS (BX)(R8*4), X0
	VADDSS X0, X15, X15

	// y[bestID] += 2
	VMOVSS (BX)(R8*4), X0
	VADDSS X9, X0, X0
	VMOVSS X0, (BX)(R8*4)

	// iy[bestID]++ (int32, 4 bytes per element)
	LEAQ  (R9)(R8*4), R11
	INCL  (R11)

	// Decrement outer counter
	DECQ  R10
	JNZ   ppl_outer

ppl_done:
	VMOVSS X14, ret+96(FP)
	VMOVSS X15, ret1+100(FP)
	VZEROUPPER
	RET

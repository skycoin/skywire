//go:build arm64 && !purego
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
//   R0  = absX base
//   R1  = y base
//   R2  = iy base
//   R3  = n
//   R4  = pulsesLeft (outer counter)
//   R5  = j (inner counter)
//   R6  = bestID
//   R7  = temp
//   F16 = xy (float32, updated per pulse)
//   F17 = yy (float32, updated per pulse)
//   F18 = bestNum
//   F19 = bestDen
//   F20 = constant 1.0f
//   F21 = constant 2.0f
//   F0-F5 = temporaries
TEXT ·pvqSearchPulseLoop(SB), NOSPLIT, $0-104
	MOVD  absX_base+0(FP), R0
	MOVD  y_base+24(FP), R1
	MOVD  iy_base+48(FP), R2
	FMOVS xy+72(FP), F16
	FMOVS yy+76(FP), F17
	MOVD  n+80(FP), R3
	MOVD  pulsesLeft+88(FP), R4

	// Load constants
	FMOVS $1.0, F20               // 1.0f
	FMOVS $2.0, F21               // 2.0f

	// If pulsesLeft <= 0 or n <= 0, return immediately
	CBZ   R4, pl_done
	CBZ   R3, pl_done

pl_outer:
	// yy += 1
	FADDS F20, F17, F17

	// Inner search: find bestID for this pulse
	// Init: position 0
	FMOVS (R0), F0                // absX[0]
	FADDS F16, F0, F0             // rxy = xy + absX[0]
	FMOVS (R1), F1                // y[0]
	FADDS F17, F1, F19            // bestDen = yy + y[0]
	FMULS F0, F0, F18             // bestNum = rxy * rxy
	MOVD  ZR, R6                  // bestID = 0

	CMP   $2, R3
	BLT   pl_update

	MOVD  $1, R5                  // j = 1

	// Check if we can do 2x unrolled loop (need j+1 < n, i.e. j < n-1)
	SUB   $1, R3, R9             // R9 = n-1
	CMP   R9, R5
	BGE   pl_inner_tail

pl_inner2:
	// --- Iteration j ---
	FMOVS (R0)(R5<<2), F0        // absX[j]
	FADDS F16, F0, F0             // rxy
	FMOVS (R1)(R5<<2), F1        // y[j]
	FADDS F17, F1, F1             // ryy
	FMULS F0, F0, F2              // num = rxy^2
	FMULS F19, F2, F3             // lhs = bestDen * num
	FMULS F1, F18, F4             // rhs = ryy * bestNum
	FCMPS F4, F3
	BLE   pl_skip1
	FMOVS F1, F19                 // bestDen = ryy
	FMOVS F2, F18                 // bestNum = num
	MOVD  R5, R6                  // bestID = j
pl_skip1:

	// --- Iteration j+1 ---
	ADD   $1, R5, R7             // R7 = j+1
	FMOVS (R0)(R7<<2), F0        // absX[j+1]
	FADDS F16, F0, F0
	FMOVS (R1)(R7<<2), F1        // y[j+1]
	FADDS F17, F1, F1
	FMULS F0, F0, F2
	FMULS F19, F2, F3
	FMULS F1, F18, F4
	FCMPS F4, F3
	BLE   pl_skip2
	FMOVS F1, F19
	FMOVS F2, F18
	MOVD  R7, R6                  // bestID = j+1
pl_skip2:

	ADD   $2, R5
	CMP   R9, R5
	BLT   pl_inner2

pl_inner_tail:
	// Handle last element if n is even (j == n-1)
	CMP   R3, R5
	BGE   pl_update

	FMOVS (R0)(R5<<2), F0
	FADDS F16, F0, F0
	FMOVS (R1)(R5<<2), F1
	FADDS F17, F1, F1
	FMULS F0, F0, F2
	FMULS F19, F2, F3
	FMULS F1, F18, F4
	FCMPS F4, F3
	BLE   pl_update
	FMOVS F1, F19
	FMOVS F2, F18
	MOVD  R5, R6

pl_update:
	// xy += absX[bestID]
	FMOVS (R0)(R6<<2), F0
	FADDS F0, F16, F16

	// yy += y[bestID]
	FMOVS (R1)(R6<<2), F0
	FADDS F0, F17, F17

	// y[bestID] += 2
	FMOVS (R1)(R6<<2), F0
	FADDS F21, F0, F0
	FMOVS F0, (R1)(R6<<2)

	// iy[bestID]++ (int32, 4 bytes per element)
	LSL   $2, R6, R7
	ADD   R2, R7
	MOVW  (R7), R8
	ADD   $1, R8
	MOVW  R8, (R7)

	// Decrement outer counter
	SUB   $1, R4
	CBNZ  R4, pl_outer

pl_done:
	FMOVS  F16, ret+96(FP)
	FMOVS  F17, ret1+100(FP)
	RET

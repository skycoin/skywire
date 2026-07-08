//go:build arm64 && !purego
#include "textflag.h"

// func celtPitchXcorrFloatImplASM(x, y []float32, out []float32, length, maxPitch int)
TEXT ·celtPitchXcorrFloatImplASM(SB), NOSPLIT, $0-88
	MOVD    x_base+0(FP), R0
	MOVD    y_base+24(FP), R1
	MOVD    out_base+48(FP), R2
	MOVD    length+72(FP), R3
	MOVD    maxPitch+80(FP), R4

	CMP     $0, R4
	BLE     done
	CMP     $0, R3
	BLE     done

	MOVD    ZR, R5          // lag = 0

outer_loop8:
	ADD     $8, R5, R10
	CMP     R10, R4
	BLT     outer_loop4

	VEOR    V0.B16, V0.B16, V0.B16 // acc0 (lags 0-3)
	VEOR    V10.B16, V10.B16, V10.B16 // acc1 (lags 4-7)

	MOVD    ZR, R6          // k = 0
	MOVD    R0, R7          // x_ptr
	LSL     $2, R5, R11
	ADD     R1, R11, R8     // y_ptr = y + lag*4

inner_loop8:
	SUB     R6, R3, R12
	CMP     $4, R12
	BLT     inner_tail8

	// Load x[k...k+3]
	VLD1.P  16(R7), [V4.B16]

	// Load y[lag+k ... lag+k+11] (3 vectors)
	WORD    $0x4c406105     // ld1 {v5.16b, v6.16b, v7.16b}, [x8]
	ADD     $16, R8

	// --- Lags 0-3 (V0) ---
	// Lag 0: V5
	WORD    $0x4f8410a0     // fmla v0.4s, v5.4s, v4.s[0]
	// Lag 1: EXT V1, V5, V6, #4
	WORD    $0x6e0620a1
	WORD    $0x4fa41020     // fmla v0.4s, v1.4s, v4.s[1]
	// Lag 2: EXT V2, V5, V6, #8
	WORD    $0x6e0640a2
	WORD    $0x4f841840     // fmla v0.4s, v2.4s, v4.s[2]
	// Lag 3: EXT V3, V5, V6, #12
	WORD    $0x6e0660a3
	WORD    $0x4fa41860     // fmla v0.4s, v3.4s, v4.s[3]

	// --- Lags 4-7 (V10) ---
	// Lag 4: V6
	WORD    $0x4f8410ca     // fmla v10.4s, v6.4s, v4.s[0]
	// Lag 5: EXT V11, V6, V7, #4
	WORD    $0x6e0720cb
	WORD    $0x4fa4116a     // fmla v10.4s, v11.4s, v4.s[1]
	// Lag 6: EXT V12, V6, V7, #8
	WORD    $0x6e0740cc
	WORD    $0x4f84198a     // fmla v10.4s, v12.4s, v4.s[2]
	// Lag 7: EXT V13, V6, V7, #12
	WORD    $0x6e0760cd
	WORD    $0x4fa419aa     // fmla v10.4s, v13.4s, v4.s[3]

	ADD     $4, R6
	B       inner_loop8

inner_tail8:
	// Handle tail for 8-lag block (scalar fallback)
	CMP     R6, R3
	BEQ     store_block8

	FMOVS   (R7), F4        // x[k]
	ADD     $4, R7
	
	// Load y[lag+k ... lag+k+7]
	// We need 8 values. V5 (4), V6 (4).
	VLD1.P  16(R8), [V5.B16]
	VLD1    (R8), [V6.B16]
	SUB     $16, R8         // Rewind R8 because we didn't advance in loop properly?
	// Wait, loop advanced R8 by 16.
	// In tail, we need to load from current R8.
	// But VLD1.P advances.
	// Let's just use VLD1 (no P) and manual add if needed.
	// Actually easier to just load V5, V6 manually.
	// V5 = y[k..k+3]. V6 = y[k+4..k+7].
	// Lag 0: y[k]. Lag 4: y[k+4].
	// FMLA V0, V5, x. FMLA V10, V6, x.
	// Wait, V0 accumulates [acc0, acc1, acc2, acc3].
	// Scalar update:
	// acc0 += x * y[k]
	// acc1 += x * y[k+1]
	// ...
	// We can use vector-scalar FMLA again.
	// x is scalar F4.
	// y vector V5.
	// FMLA V0, V5, V4.S[0]
	// FMLA V10, V6, V4.S[0]
	// But x[k] is scalar. V4 is scalar (loaded F4).
	// We can use FMLA.4S V0, V5, V4[0].
	// Need to move F4 to V4 element 0.
	
	// Let's do a simple scalar loop for tail logic.
	// Or utilize the vector accumulators.
	// V0[0] += x * y[k]
	// V0[1] += x * y[k+1]
	// ...
	// y[k..k+3] is V5.
	// y[k+4..k+7] is V6.
	// Multiply V5 by x. Add to V0.
	// Multiply V6 by x. Add to V10.
	// Correct.
	
	// Reload y properly.
	// R8 points to y[lag+k].
	VLD1.P  16(R8), [V5.B16]
	VLD1.P  16(R8), [V6.B16]
	// V4 has x[k].
	// FMLA V0.4S, V5.4S, V4.S[0]
	WORD    $0x4f8410a0
	// FMLA V10.4S, V6.4S, V4.S[0]
	WORD    $0x4f8410ca
	
	ADD     $1, R6
	B       inner_tail8

store_block8:
	VST1.P  [V0.B16], 16(R2)
	VST1.P  [V10.B16], 16(R2)
	ADD     $8, R5
	B       outer_loop8

outer_loop4:
	// Existing 4-lag logic
	ADD     $4, R5, R10
	CMP     R10, R4
	BLT     outer_tail_scalar

	VEOR    V0.B16, V0.B16, V0.B16
	MOVD    ZR, R6
	MOVD    R0, R7
	LSL     $2, R5, R11
	ADD     R1, R11, R8

inner_loop4:
	SUB     R6, R3, R12
	CMP     $4, R12
	BLT     inner_tail4

	VLD1.P  16(R7), [V4.B16]
	VLD1.P  16(R8), [V5.B16]
	VLD1    (R8), [V6.B16]

	WORD    $0x4f8410a0 // FMLA
	WORD    $0x6e0620a1 // EXT
	WORD    $0x4fa41020 // FMLA
	WORD    $0x6e0640a2
	WORD    $0x4f841840
	WORD    $0x6e0660a3
	WORD    $0x4fa41860

	ADD     $4, R6
	B       inner_loop4

inner_tail4:
	CMP     R6, R3
	BEQ     store_block4

	FMOVS   (R7), F4
	ADD     $4, R7
	VLD1    (R8), [V5.B16]
	ADD     $4, R8
	WORD    $0x4f8410a0 // FMLA
	
	ADD     $1, R6
	B       inner_tail4

store_block4:
	VST1.P  [V0.B16], 16(R2)
	ADD     $4, R5
	B       outer_loop8 // Check if we can do more 8s? No, we are at tail.
	// Go to outer_tail_scalar

outer_tail_scalar:
	// Scalar fallback for remaining <4 lags
	CMP     R5, R4
	BEQ     done

	MOVD    ZR, R6
	FMOVS   ZR, F0
	MOVD    R0, R7
	LSL     $2, R5, R11
	ADD     R1, R11, R8

single_lag_loop:
	CMP     R6, R3
	BEQ     single_lag_store

	FMOVS   (R7), F1
	ADD     $4, R7
	FMOVS   (R8), F2
	ADD     $4, R8
	
	FMULS   F1, F2, F3
	FADDS   F3, F0, F0
	ADD     $1, R6
	B       single_lag_loop

single_lag_store:
	FMOVS   F0, (R2)
	ADD     $4, R2
	ADD     $1, R5
	B       outer_tail_scalar

done:
	RET

//go:build arm64 && !purego
#include "textflag.h"

// All three butterfly inner loops for ARM64.
// On ARM64, kissFFTFMALikeEnabled=true, so complex twiddle multiply uses:
//   kissMulSubSource(a,b,c,d) = round(a*b - round(c*d))  → FMULS + FNEGS + FMADDS
//   kissMulAddSource(a,b,c,d) = round(a*b + round(c*d))  → FMULS + FMADDS
// kissCpx is {r float32, i float32} = 8 bytes, no padding.

// func kfBfly5Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int)
//
// Radix-5 butterfly inner loop. Computes ya = w[fstride*m], yb = w[fstride*2*m].
// Eliminates all noinline function call overhead.
//
// Register allocation:
//   R0  = fout base ptr            F0,F1   = s0.r, s0.i
//   R1  = w base ptr               F2,F3   = s1/s7 .r,.i
//   R2  = m                        F4,F5   = s2/s8 .r,.i
//   R3  = N                        F6,F7   = s3 .r,.i / scratch
//   R4  = mm                       F16,F17 = s4/s10 .r,.i → scratch
//   R5  = fstride                  F18,F19 = s10/s9 .r,.i
//   R6  = outer counter            F20,F21 = s9 .r,.i
//   R7  = inner counter            F22,F23 = scratch (s6, s12)
//   R8  = fout idx0 byte ptr       F24     = yar
//   R9  = m*8 (byte stride)        F25     = yai
//   R10 = tw1 byte offset          F26     = ybr
//   R11 = fstride*8                F27     = ybi
//   R12 = fstride2*8               F28-F31 = scratch
//   R13 = fstride3*8
//   R14 = fstride4*8
//   R15 = temp
TEXT ·kfBfly5Inner(SB), NOSPLIT, $0-80
	MOVD fout_base+0(FP), R0
	MOVD w_base+24(FP), R1
	MOVD m+48(FP), R2
	MOVD N+56(FP), R3
	MOVD mm+64(FP), R4
	MOVD fstride+72(FP), R5

	// Compute ya = w[fstride*m], yb = w[fstride*2*m]
	MUL  R5, R2, R6         // R6 = fstride*m
	LSL  $3, R6, R7         // R7 = fstride*m*8 (byte offset, kissCpx=8 bytes)
	ADD  R1, R7, R7         // R7 = &w[fstride*m]
	FMOVS (R7), F24         // yar = w[fstride*m].r
	FMOVS 4(R7), F25        // yai = w[fstride*m].i
	LSL  $1, R6, R6         // R6 = fstride*2*m
	LSL  $3, R6, R7
	ADD  R1, R7, R7         // R7 = &w[fstride*2*m]
	FMOVS (R7), F26         // ybr = w[fstride*2*m].r
	FMOVS 4(R7), F27        // ybi = w[fstride*2*m].i

	// Precompute byte strides
	LSL  $3, R2, R9         // R9 = m*8 (byte distance between butterfly elements)
	LSL  $3, R5, R11        // R11 = fstride*8 (tw stride in bytes)
	LSL  $1, R11, R12       // R12 = fstride*2*8
	ADD  R11, R12, R13      // R13 = fstride*3*8
	LSL  $1, R12, R14       // R14 = fstride*4*8

	// mm*8 for outer loop base advancement
	LSL  $3, R4, R4         // R4 = mm*8

	MOVD ZR, R6             // outer counter i = 0

bfly5_outer:
	CMP  R3, R6
	BGE  bfly5_done

	// R8 = &fout[i*mm] = R0 + i*mm*8
	MUL  R6, R4, R8         // R8 = i * mm_bytes
	ADD  R0, R8, R8         // R8 = &fout[base] (idx0 ptr)

	MOVD ZR, R10            // tw1 byte offset = 0
	MOVD R2, R7             // inner counter = m

bfly5_inner:
	// Load s0 = fout[idx0]
	FMOVS (R8), F0          // s0.r
	FMOVS 4(R8), F1         // s0.i

	// Load b1 = fout[idx1] = fout[idx0 + m]
	ADD  R9, R8, R15        // R15 = &fout[idx1]
	FMOVS (R15), F2         // b1.r
	FMOVS 4(R15), F3        // b1.i

	// Load w1 = w[tw1]
	ADD  R1, R10, R16       // R16 = &w[tw1]
	FMOVS (R16), F28        // w1.r
	FMOVS 4(R16), F29       // w1.i

	// s1 = cmul(b1, w1) with FMALike
	// s1.r = kissMulSubSource(b1.r, w1.r, b1.i, w1.i) = round(b1.r*w1.r - round(b1.i*w1.i))
	FMULS F3, F29, F30      // F30 = round(b1.i * w1.i)
	FNEGS F30, F30           // F30 = -round(b1.i * w1.i)
	FMADDS F28, F30, F2, F30 // F30 = round(F30 + b1.r * w1.r) = round(b1.r*w1.r - round(b1.i*w1.i))
	// s1.i = kissMulAddSource(b1.r, w1.i, b1.i, w1.r) = round(b1.r*w1.i + round(b1.i*w1.r))
	FMULS F3, F28, F31      // F31 = round(b1.i * w1.r)
	FMADDS F29, F31, F2, F31 // F31 = round(F31 + b1.r * w1.i)
	FMOVS F30, F2           // s1.r
	FMOVS F31, F3           // s1.i

	// Load b2 = fout[idx2] = fout[idx0 + 2m]
	ADD  R9, R15, R15       // R15 = &fout[idx2]
	FMOVS (R15), F4         // b2.r
	FMOVS 4(R15), F5        // b2.i

	// Load w2 = w[tw2] (tw2 = 2*tw1)
	LSL  $1, R10, R16       // R16 = 2 * tw1_bytes
	ADD  R1, R16, R16       // R16 = &w[tw2]
	FMOVS (R16), F28
	FMOVS 4(R16), F29

	// s2 = cmul(b2, w2)
	FMULS F5, F29, F30
	FNEGS F30, F30
	FMADDS F28, F30, F4, F30
	FMULS F5, F28, F31
	FMADDS F29, F31, F4, F31
	FMOVS F30, F4           // s2.r
	FMOVS F31, F5           // s2.i

	// Load b3 = fout[idx3] = fout[idx0 + 3m]
	ADD  R9, R15, R15       // R15 = &fout[idx3]
	FMOVS (R15), F6         // b3.r
	FMOVS 4(R15), F7        // b3.i

	// Load w3 = w[tw3] (tw3 = 3*tw1)
	LSL  $1, R10, R16       // R16 = 2 * tw1_bytes
	ADD  R10, R16, R16      // R16 = 3 * tw1_bytes
	ADD  R1, R16, R16
	FMOVS (R16), F28
	FMOVS 4(R16), F29

	// s3 = cmul(b3, w3)
	FMULS F7, F29, F30
	FNEGS F30, F30
	FMADDS F28, F30, F6, F30
	FMULS F7, F28, F31
	FMADDS F29, F31, F6, F31
	FMOVS F30, F6           // s3.r
	FMOVS F31, F7           // s3.i

	// Load b4 = fout[idx4] = fout[idx0 + 4m]
	ADD  R9, R15, R15       // R15 = &fout[idx4]
	FMOVS (R15), F16        // b4.r
	FMOVS 4(R15), F17       // b4.i

	// Load w4 = w[tw4] (tw4 = 4*tw1)
	LSL  $2, R10, R16       // R16 = 4 * tw1_bytes
	ADD  R1, R16, R16
	FMOVS (R16), F28
	FMOVS 4(R16), F29

	// s4 = cmul(b4, w4)
	FMULS F17, F29, F30
	FNEGS F30, F30
	FMADDS F28, F30, F16, F30
	FMULS F17, F28, F31
	FMADDS F29, F31, F16, F31
	FMOVS F30, F16          // s4.r
	FMOVS F31, F17          // s4.i

	// Compute s10 = s1 - s4 (before overwriting s1 with s7)
	FSUBS F16, F2, F18      // s10.r = s1.r - s4.r
	FSUBS F17, F3, F19      // s10.i = s1.i - s4.i

	// s7 = s1 + s4
	FADDS F16, F2, F2       // s7.r = s1.r + s4.r (overwrite F2)
	FADDS F17, F3, F3       // s7.i = s1.i + s4.i (overwrite F3)

	// Compute s9 = s2 - s3 (before overwriting s2 with s8)
	FSUBS F6, F4, F20       // s9.r = s2.r - s3.r
	FSUBS F7, F5, F21       // s9.i = s2.i - s3.i

	// s8 = s2 + s3
	FADDS F6, F4, F4        // s8.r = s2.r + s3.r (overwrite F4)
	FADDS F7, F5, F5        // s8.i = s2.i + s3.i (overwrite F5)

	// Now: F0,F1=s0  F2,F3=s7  F4,F5=s8  F18,F19=s10  F20,F21=s9
	// Constants: F24=yar  F25=yai  F26=ybr  F27=ybi

	// fout[idx0].r = kissAdd(s0.r, kissAdd(s7.r, s8.r))
	FADDS F4, F2, F6        // F6 = s7.r + s8.r
	FADDS F6, F0, F6        // F6 = s0.r + (s7.r + s8.r)
	// fout[idx0].i = kissAdd(s0.i, kissAdd(s7.i, s8.i))
	FADDS F5, F3, F7        // F7 = s7.i + s8.i
	FADDS F7, F1, F7        // F7 = s0.i + (s7.i + s8.i)
	FMOVS F6, (R8)          // store fout[idx0].r
	FMOVS F7, 4(R8)         // store fout[idx0].i

	// s5.r = kissAdd(s0.r, kissMulAddSource(s7.r, yar, s8.r, ybr))
	// kissMulAddSource(s7r, yar, s8r, ybr) = round(s7r*yar + round(s8r*ybr))
	FMULS F4, F26, F16      // F16 = round(s8.r * ybr)
	FMADDS F24, F16, F2, F16 // F16 = round(F16 + s7.r * yar)
	FADDS F16, F0, F16      // s5.r = s0.r + result

	// s5.i = kissAdd(s0.i, kissMulAddSource(s7.i, yar, s8.i, ybr))
	FMULS F5, F26, F17      // F17 = round(s8.i * ybr)
	FMADDS F24, F17, F3, F17 // F17 = round(F17 + s7.i * yar)
	FADDS F17, F1, F17      // s5.i = s0.i + result

	// s6.r = kissMulAddSource(s10.i, yai, s9.i, ybi)
	// = round(s10.i*yai + round(s9.i*ybi))
	FMULS F21, F27, F22     // F22 = round(s9.i * ybi)
	FMADDS F25, F22, F19, F22 // F22 = round(F22 + s10.i * yai)

	// s6.i = -kissMulAddSource(s10.r, yai, s9.r, ybi)
	// = -round(s10.r*yai + round(s9.r*ybi))
	FMULS F20, F27, F23     // F23 = round(s9.r * ybi)
	FMADDS F25, F23, F18, F23 // F23 = round(F23 + s10.r * yai)
	FNEGS F23, F23           // s6.i = -result

	// fout[idx1] = s5 - s6
	FSUBS F22, F16, F6      // f1.r = s5.r - s6.r
	FSUBS F23, F17, F7      // f1.i = s5.i - s6.i
	ADD  R9, R8, R15        // R15 = &fout[idx1]
	FMOVS F6, (R15)
	FMOVS F7, 4(R15)

	// fout[idx4] = s5 + s6
	FADDS F22, F16, F6      // f4.r = s5.r + s6.r
	FADDS F23, F17, F7      // f4.i = s5.i + s6.i
	ADD  R9, R15, R15       // idx2
	ADD  R9, R15, R15       // idx3
	ADD  R9, R15, R15       // idx4
	FMOVS F6, (R15)
	FMOVS F7, 4(R15)

	// s11.r = kissAdd(s0.r, kissMulAddSource(s7.r, ybr, s8.r, yar))
	FMULS F4, F24, F16      // F16 = round(s8.r * yar)
	FMADDS F26, F16, F2, F16 // F16 = round(F16 + s7.r * ybr)
	FADDS F16, F0, F16      // s11.r = s0.r + result

	// s11.i = kissAdd(s0.i, kissMulAddSource(s7.i, ybr, s8.i, yar))
	FMULS F5, F24, F17      // F17 = round(s8.i * yar)
	FMADDS F26, F17, F3, F17 // F17 = round(F17 + s7.i * ybr)
	FADDS F17, F1, F17      // s11.i = s0.i + result

	// s12.r = kissMulSubSource(s9.i, yai, s10.i, ybi)
	// = round(s9.i*yai - round(s10.i*ybi))
	FMULS F19, F27, F22     // F22 = round(s10.i * ybi)
	FNEGS F22, F22
	FMADDS F25, F22, F21, F22 // F22 = round(-round(s10.i*ybi) + s9.i * yai)

	// s12.i = kissMulSubSource(s10.r, ybi, s9.r, yai)
	// = round(s10.r*ybi - round(s9.r*yai))
	FMULS F20, F25, F23     // F23 = round(s9.r * yai)
	FNEGS F23, F23
	FMADDS F27, F23, F18, F23 // F23 = round(-round(s9.r*yai) + s10.r * ybi)

	// fout[idx2] = s11 + s12
	FADDS F22, F16, F6
	FADDS F23, F17, F7
	ADD  R9, R8, R15        // idx1
	ADD  R9, R15, R15       // idx2
	FMOVS F6, (R15)
	FMOVS F7, 4(R15)

	// fout[idx3] = s11 - s12
	FSUBS F22, F16, F6
	FSUBS F23, F17, F7
	ADD  R9, R15, R15       // idx3
	FMOVS F6, (R15)
	FMOVS F7, 4(R15)

	// Advance inner loop
	ADD  $8, R8, R8         // idx0++ (next kissCpx = +8 bytes)
	ADD  R11, R10, R10      // tw1 += fstride*8
	SUBS $1, R7, R7
	BNE  bfly5_inner

	// Advance outer loop
	ADD  $1, R6, R6
	B    bfly5_outer

bfly5_done:
	RET

// func kfBfly3Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int)
//
// Radix-3 butterfly inner loop. Computes epi3i = w[fstride*m].i internally.
TEXT ·kfBfly3Inner(SB), NOSPLIT, $0-80
	MOVD fout_base+0(FP), R0
	MOVD w_base+24(FP), R1
	MOVD m+48(FP), R2
	MOVD N+56(FP), R3
	MOVD mm+64(FP), R4
	MOVD fstride+72(FP), R5

	// Compute epi3i = w[fstride*m].i
	MUL  R5, R2, R6
	LSL  $3, R6, R6
	ADD  R1, R6, R6
	FMOVS 4(R6), F24        // epi3i = w[fstride*m].i

	// Precompute byte strides
	LSL  $3, R2, R9         // R9 = m*8
	LSL  $3, R5, R11        // R11 = fstride*8
	LSL  $1, R11, R12       // R12 = fstride*2*8

	// mm*8 for outer loop
	LSL  $3, R4, R4

	// half constant for kissHalfSub
	FMOVS $0.5, F25

	MOVD ZR, R6             // outer i = 0

bfly3_outer:
	CMP  R3, R6
	BGE  bfly3_done

	MUL  R6, R4, R8
	ADD  R0, R8, R8         // R8 = &fout[base]

	MOVD ZR, R10            // tw1 byte offset = 0
	MOVD R2, R7             // inner counter = m

bfly3_inner:
	// Load a0 = fout[idx0]
	FMOVS (R8), F0          // a0.r
	FMOVS 4(R8), F1         // a0.i

	// Load b1 = fout[idx1]
	ADD  R9, R8, R15
	FMOVS (R15), F2         // b1.r
	FMOVS 4(R15), F3        // b1.i

	// Load w1 = w[tw1]
	ADD  R1, R10, R16
	FMOVS (R16), F28
	FMOVS 4(R16), F29

	// s1 = cmul(b1, w1) — FMALike
	FMULS F3, F29, F30
	FNEGS F30, F30
	FMADDS F28, F30, F2, F30
	FMULS F3, F28, F31
	FMADDS F29, F31, F2, F31
	// F30 = s1.r, F31 = s1.i

	// Load b2 = fout[idx2]
	ADD  R9, R15, R15
	FMOVS (R15), F4         // b2.r
	FMOVS 4(R15), F5        // b2.i

	// Load w2 = w[tw2] (tw2 = 2*tw1)
	LSL  $1, R10, R16       // R16 = 2 * tw1_bytes
	ADD  R1, R16, R16
	FMOVS (R16), F28
	FMOVS 4(R16), F29

	// s2 = cmul(b2, w2) — FMALike
	FMULS F5, F29, F16
	FNEGS F16, F16
	FMADDS F28, F16, F4, F16
	FMULS F5, F28, F17
	FMADDS F29, F17, F4, F17
	// F16 = s2.r, F17 = s2.i

	// s3 = s1 + s2
	FADDS F16, F30, F2      // s3.r
	FADDS F17, F31, F3      // s3.i

	// s0 = s1 - s2
	FSUBS F16, F30, F4      // s0.r
	FSUBS F17, F31, F5      // s0.i

	// fout[idx1].r = kissHalfSub(a0.r, s3.r) = a0.r - 0.5*s3.r
	FMULS F25, F2, F6       // 0.5*s3.r
	FSUBS F6, F0, F6        // a0.r - 0.5*s3.r
	// fout[idx1].i = kissHalfSub(a0.i, s3.i) = a0.i - 0.5*s3.i
	FMULS F25, F3, F7       // 0.5*s3.i
	FSUBS F7, F1, F7        // a0.i - 0.5*s3.i

	// s0.r = kissScaleMul(s0.r, epi3i)
	FMULS F24, F4, F4
	// s0.i = kissScaleMul(s0.i, epi3i)
	FMULS F24, F5, F5

	// fout[idx0] = a0 + s3
	FADDS F2, F0, F16
	FADDS F3, F1, F17
	FMOVS F16, (R8)
	FMOVS F17, 4(R8)

	// fout[idx2].r = fout[idx1].r + s0.i
	FADDS F5, F6, F16
	// fout[idx2].i = fout[idx1].i - s0.r
	FSUBS F4, F7, F17
	ADD  R9, R8, R15        // &fout[idx1]
	ADD  R9, R15, R16       // &fout[idx2]
	FMOVS F16, (R16)
	FMOVS F17, 4(R16)

	// fout[idx1].r = fout[idx1].r - s0.i
	FSUBS F5, F6, F16
	// fout[idx1].i = fout[idx1].i + s0.r
	FADDS F4, F7, F17
	FMOVS F16, (R15)
	FMOVS F17, 4(R15)

	// Advance
	ADD  $8, R8, R8
	ADD  R11, R10, R10
	SUBS $1, R7, R7
	BNE  bfly3_inner

	ADD  $1, R6, R6
	B    bfly3_outer

bfly3_done:
	RET

// func kfBfly4Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int)
//
// Radix-4 butterfly inner loop.
TEXT ·kfBfly4Inner(SB), NOSPLIT, $0-80
	MOVD fout_base+0(FP), R0
	MOVD w_base+24(FP), R1
	MOVD m+48(FP), R2
	MOVD N+56(FP), R3
	MOVD mm+64(FP), R4
	MOVD fstride+72(FP), R5

	// Precompute byte strides
	LSL  $3, R2, R9         // R9 = m*8
	LSL  $3, R5, R11        // R11 = fstride*8
	LSL  $1, R11, R12       // R12 = fstride*2*8
	ADD  R11, R12, R13      // R13 = fstride*3*8

	LSL  $3, R4, R4         // mm*8

	MOVD ZR, R6             // outer i = 0

bfly4_outer:
	CMP  R3, R6
	BGE  bfly4_done

	MUL  R6, R4, R8
	ADD  R0, R8, R8         // R8 = &fout[base]

	MOVD ZR, R10            // tw1 byte offset = 0
	MOVD R2, R7             // inner counter = m

bfly4_inner:
	// Load f0 = fout[idx0]
	FMOVS (R8), F0          // f0.r
	FMOVS 4(R8), F1         // f0.i

	// Load b1 = fout[idx1], w1
	ADD  R9, R8, R15
	FMOVS (R15), F2
	FMOVS 4(R15), F3
	ADD  R1, R10, R16
	FMOVS (R16), F28
	FMOVS 4(R16), F29

	// s0 = cmul(b1, w1) — FMALike
	FMULS F3, F29, F30
	FNEGS F30, F30
	FMADDS F28, F30, F2, F4 // s0.r
	FMULS F3, F28, F30
	FMADDS F29, F30, F2, F5 // s0.i

	// Load b2 = fout[idx2], w2
	ADD  R9, R15, R15
	FMOVS (R15), F2
	FMOVS 4(R15), F3
	LSL  $1, R10, R16       // R16 = 2 * tw1_bytes
	ADD  R1, R16, R16
	FMOVS (R16), F28
	FMOVS 4(R16), F29

	// s1 = cmul(b2, w2)
	FMULS F3, F29, F30
	FNEGS F30, F30
	FMADDS F28, F30, F2, F6 // s1.r
	FMULS F3, F28, F30
	FMADDS F29, F30, F2, F7 // s1.i

	// Load b3 = fout[idx3], w3
	ADD  R9, R15, R15
	FMOVS (R15), F2
	FMOVS 4(R15), F3
	LSL  $1, R10, R16       // R16 = 2 * tw1_bytes
	ADD  R10, R16, R16      // R16 = 3 * tw1_bytes
	ADD  R1, R16, R16
	FMOVS (R16), F28
	FMOVS 4(R16), F29

	// s2 = cmul(b3, w3)
	FMULS F3, F29, F30
	FNEGS F30, F30
	FMADDS F28, F30, F2, F16 // s2.r
	FMULS F3, F28, F30
	FMADDS F29, F30, F2, F17 // s2.i

	// Butterfly combinations:
	// s5 = f0 - s1
	FSUBS F6, F0, F18       // s5.r
	FSUBS F7, F1, F19       // s5.i
	// f0 += s1
	FADDS F6, F0, F0
	FADDS F7, F1, F1

	// s3 = s0 + s2
	FADDS F16, F4, F2       // s3.r
	FADDS F17, F5, F3       // s3.i
	// s4 = s0 - s2
	FSUBS F16, F4, F20      // s4.r
	FSUBS F17, F5, F21      // s4.i

	// fout[idx2] = f0 - s3
	FSUBS F2, F0, F6
	FSUBS F3, F1, F7
	ADD  R9, R8, R15        // idx1
	ADD  R9, R15, R16       // idx2
	FMOVS F6, (R16)
	FMOVS F7, 4(R16)

	// fout[idx0] = f0 + s3 (f0 already has += s1, so f0 + s3)
	FADDS F2, F0, F6
	FADDS F3, F1, F7
	FMOVS F6, (R8)
	FMOVS F7, 4(R8)

	// fout[idx1] = s5.r + s4.i, s5.i - s4.r
	FADDS F21, F18, F6
	FSUBS F20, F19, F7
	FMOVS F6, (R15)
	FMOVS F7, 4(R15)

	// fout[idx3] = s5.r - s4.i, s5.i + s4.r
	FSUBS F21, F18, F6
	FADDS F20, F19, F7
	ADD  R9, R16, R16       // idx3
	FMOVS F6, (R16)
	FMOVS F7, 4(R16)

	// Advance
	ADD  $8, R8, R8
	ADD  R11, R10, R10
	SUBS $1, R7, R7
	BNE  bfly4_inner

	ADD  $1, R6, R6
	B    bfly4_outer

bfly4_done:
	RET

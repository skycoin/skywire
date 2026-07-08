//go:build amd64 && !purego
#include "textflag.h"

// All three butterfly inner loops for AMD64.
// On AMD64, kissFFTFMALikeEnabled=false, so complex twiddle multiply uses:
//   kissMulSubSource(a,b,c,d) = a*b - c*d  (separate MULSS then SUBSS)
//   kissMulAddSource(a,b,c,d) = a*b + c*d  (separate MULSS then ADDSS)
// kissCpx is {r float32, i float32} = 8 bytes.

// func kfBfly5Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int)
TEXT ·kfBfly5Inner(SB), NOSPLIT, $0-80
	MOVQ fout_base+0(FP), DI
	MOVQ w_base+24(FP), SI
	MOVQ m+48(FP), DX
	MOVQ N+56(FP), CX
	MOVQ mm+64(FP), R8
	MOVQ fstride+72(FP), R9

	// Compute ya = w[fstride*m], yb = w[fstride*2*m]
	MOVQ R9, AX
	IMULQ DX, AX            // AX = fstride*m
	MOVQ AX, R15
	SHLQ $3, R15            // R15 = fstride*m*8
	MOVSS (SI)(R15*1), X12   // yar
	MOVSS 4(SI)(R15*1), X13  // yai
	SHLQ $1, AX             // AX = fstride*2*m
	SHLQ $3, AX
	MOVSS (SI)(AX*1), X14    // ybr
	MOVSS 4(SI)(AX*1), X15   // ybi

	// Precompute byte strides
	MOVQ DX, R10
	SHLQ $3, R10            // R10 = m*8
	MOVQ R9, R11
	SHLQ $3, R11            // R11 = fstride*8
	MOVQ R11, R12
	SHLQ $1, R12            // R12 = fstride*2*8
	LEAQ (R11)(R12*1), R13  // R13 = fstride*3*8
	SHLQ $1, R12
	MOVQ R12, R14            // R14 = fstride*4*8
	MOVQ R9, R12
	SHLQ $4, R12            // R12 = fstride*2*8 (recompute)

	// mm*8 for outer loop
	SHLQ $3, R8

	XORQ AX, AX             // outer i = 0

bfly5_outer:
	CMPQ AX, CX
	JGE  bfly5_done

	MOVQ AX, BX
	IMULQ R8, BX
	LEAQ (DI)(BX*1), BX     // BX = &fout[base]

	XORQ R15, R15           // tw1 byte offset = 0
	MOVQ DX, R9             // inner counter = m (reuse R9, fstride saved in strides)

bfly5_inner:
	// This is the critical inner loop. We process one butterfly per iteration.
	// Load s0 = fout[idx0]
	MOVSS (BX), X0           // s0.r
	MOVSS 4(BX), X1          // s0.i

	// Load b1 = fout[idx1]
	MOVSS (BX)(R10*1), X2    // b1.r
	MOVSS 4(BX)(R10*1), X3   // b1.i

	// Load w1 = w[tw1]
	MOVSS (SI)(R15*1), X4    // w1.r
	MOVSS 4(SI)(R15*1), X5   // w1.i

	// s1 = cmul(b1, w1) — simple path: a*b - c*d, a*b + c*d
	// s1.r = b1.r*w1.r - b1.i*w1.i
	MOVSS X2, X6
	MULSS X4, X6             // b1.r*w1.r
	MOVSS X3, X7
	MULSS X5, X7             // b1.i*w1.i
	SUBSS X7, X6             // s1.r
	// s1.i = b1.r*w1.i + b1.i*w1.r
	MOVSS X2, X7
	MULSS X5, X7             // b1.r*w1.i
	MOVSS X3, X8
	MULSS X4, X8             // b1.i*w1.r
	ADDSS X8, X7             // s1.i
	MOVSS X6, X2             // s1.r → X2
	MOVSS X7, X3             // s1.i → X3

	// Load b2, w2 and compute s2
	LEAQ (BX)(R10*2), R9     // temp: &fout[idx2]
	MOVSS (R9), X4
	MOVSS 4(R9), X5
	LEAQ (R15)(R12*1), R9    // tw2 offset (use fstride*2*8 = R12)
	// Actually R12 was overwritten. Let me use R11 based approach.
	// tw2 = tw1 + fstride*8 ... no, tw2 = 2*tw1 at start, but tw increments differ.
	// tw1 starts at 0, increments by fstride*8.
	// tw2 starts at 0, increments by fstride*2*8.
	// So tw2_offset = tw1_offset * 2 is only true at start.
	// Actually: tw1 = u*fstride*8, tw2 = u*fstride*2*8 = 2*tw1. Always.
	MOVQ R15, R9
	SHLQ $1, R9              // tw2 = 2*tw1
	MOVSS (SI)(R9*1), X6
	MOVSS 4(SI)(R9*1), X7
	// s2 = cmul(b2, w2)
	MOVSS X4, X8
	MULSS X6, X8
	MOVSS X5, X9
	MULSS X7, X9
	SUBSS X9, X8             // s2.r
	MOVSS X4, X9
	MULSS X7, X9
	MOVSS X5, X10
	MULSS X6, X10
	ADDSS X10, X9            // s2.i
	MOVSS X8, X4             // s2.r → X4
	MOVSS X9, X5             // s2.i → X5

	// Load b3 = fout[idx3], compute w3 offset
	LEAQ (R10)(R10*2), R9    // R9 = 3*m*8
	MOVSS (BX)(R9*1), X6
	MOVSS 4(BX)(R9*1), X7
	// tw3 = 3*tw1
	MOVQ R15, R9
	LEAQ (R9)(R9*2), R9      // tw3 = 3*tw1
	MOVSS (SI)(R9*1), X8
	MOVSS 4(SI)(R9*1), X9
	// s3 = cmul(b3, w3)
	MOVSS X6, X10
	MULSS X8, X10
	MOVSS X7, X11
	MULSS X9, X11
	SUBSS X11, X10           // s3.r
	MOVSS X6, X11
	MULSS X9, X11
	MOVSS X7, X6
	MULSS X8, X6
	ADDSS X6, X11            // s3.i
	MOVSS X10, X6            // s3.r → X6
	MOVSS X11, X7            // s3.i → X7

	// Load b4 = fout[idx4]
	MOVQ R10, R9
	SHLQ $2, R9              // R9 = 4*m*8
	MOVSS (BX)(R9*1), X8
	MOVSS 4(BX)(R9*1), X9
	// tw4 = 4*tw1
	MOVQ R15, R9
	SHLQ $2, R9
	MOVSS (SI)(R9*1), X10
	MOVSS 4(SI)(R9*1), X11
	// s4 = cmul(b4, w4)
	// X8,X9 = b4; X10,X11 = w4.
	// Must preserve X2=s1.r, X3=s1.i, X4=s2.r, X5=s2.i, X6=s3.r, X7=s3.i.
	// Spill s0 to stack to free X0,X1 for s4 result.

	// Spill s0 to stack to free X0,X1
	SUBQ $16, SP
	MOVSS X0, (SP)           // spill s0.r
	MOVSS X1, 4(SP)          // spill s0.i

	// s4 = cmul(b4, w4) → X0, X1
	MOVSS X8, X0
	MULSS X10, X0            // b4.r*w4.r
	MOVSS X9, X1
	MULSS X11, X1            // b4.i*w4.i
	SUBSS X1, X0             // s4.r
	MOVSS X8, X1
	MULSS X11, X1            // b4.r*w4.i
	MULSS X10, X9            // b4.i*w4.r
	ADDSS X9, X1             // s4.i
	// X0=s4.r, X1=s4.i

	// Compute s7, s10, s8, s9
	// s10 = s1 - s4
	MOVSS X2, X8
	SUBSS X0, X8             // s10.r = s1.r - s4.r
	MOVSS X3, X9
	SUBSS X1, X9             // s10.i = s1.i - s4.i
	// s7 = s1 + s4
	ADDSS X0, X2             // s7.r (overwrite s1.r)
	ADDSS X1, X3             // s7.i
	// s9 = s2 - s3
	MOVSS X4, X10
	SUBSS X6, X10            // s9.r = s2.r - s3.r
	MOVSS X5, X11
	SUBSS X7, X11            // s9.i = s2.i - s3.i
	// s8 = s2 + s3
	ADDSS X6, X4             // s8.r
	ADDSS X7, X5             // s8.i

	// Restore s0
	MOVSS (SP), X0           // s0.r
	MOVSS 4(SP), X1          // s0.i
	ADDQ $16, SP

	// Now: X0,X1=s0  X2,X3=s7  X4,X5=s8  X8,X9=s10  X10,X11=s9
	// Constants: X12=yar X13=yai X14=ybr X15=ybi

	// fout[idx0] = s0 + s7 + s8
	MOVSS X2, X6
	ADDSS X4, X6             // s7.r + s8.r
	ADDSS X0, X6             // s0.r + s7.r + s8.r
	MOVSS X3, X7
	ADDSS X5, X7
	ADDSS X1, X7
	MOVSS X6, (BX)
	MOVSS X7, 4(BX)

	// Reserve 32 bytes of stack for s5, s6, s10 spills (register pressure).

	SUBQ $32, SP

	// Compute s5.r = s0.r + (s7.r*yar + s8.r*ybr)
	MOVSS X2, X6
	MULSS X12, X6            // s7.r*yar
	MOVSS X4, X7
	MULSS X14, X7            // s8.r*ybr
	ADDSS X7, X6
	ADDSS X0, X6
	MOVSS X6, (SP)           // s5.r → stack[0]

	// s5.i = s0.i + (s7.i*yar + s8.i*ybr)
	MOVSS X3, X6
	MULSS X12, X6
	MOVSS X5, X7
	MULSS X14, X7
	ADDSS X7, X6
	ADDSS X1, X6
	MOVSS X6, 4(SP)          // s5.i → stack[4]

	// s6.r = s10.i*yai + s9.i*ybi
	MOVSS X9, X6
	MULSS X13, X6            // s10.i*yai
	MOVSS X11, X7
	MULSS X15, X7            // s9.i*ybi
	ADDSS X7, X6
	MOVSS X6, 8(SP)          // s6.r → stack[8]

	// s6.i = -(s10.r*yai + s9.r*ybi)
	MOVSS X8, X6
	MULSS X13, X6            // s10.r*yai
	MOVSS X10, X7
	MULSS X15, X7            // s9.r*ybi
	ADDSS X7, X6
	MOVSS X6, X7
	XORPS X6, X6
	SUBSS X7, X6             // negate
	MOVSS X6, 12(SP)         // s6.i → stack[12]

	// fout[idx1] = s5 - s6
	MOVSS (SP), X6           // s5.r
	SUBSS 8(SP), X6          // s5.r - s6.r
	MOVSS 4(SP), X7          // s5.i
	SUBSS 12(SP), X7         // s5.i - s6.i
	MOVSS X6, (BX)(R10*1)
	MOVSS X7, 4(BX)(R10*1)

	// fout[idx4] = s5 + s6
	MOVSS (SP), X6
	ADDSS 8(SP), X6
	MOVSS 4(SP), X7
	ADDSS 12(SP), X7
	MOVQ R10, R9
	SHLQ $2, R9              // 4*m*8
	MOVSS X6, (BX)(R9*1)
	MOVSS X7, 4(BX)(R9*1)

	// s11.r = s0.r + (s7.r*ybr + s8.r*yar)
	MOVSS X2, X6
	MULSS X14, X6            // s7.r*ybr
	MOVSS X4, X7
	MULSS X12, X7            // s8.r*yar
	ADDSS X7, X6
	ADDSS X0, X6
	MOVSS X6, (SP)           // s11.r → stack[0]

	// s11.i
	MOVSS X3, X6
	MULSS X14, X6
	MOVSS X5, X7
	MULSS X12, X7
	ADDSS X7, X6
	ADDSS X1, X6
	MOVSS X6, 4(SP)          // s11.i

	// s12.r = s9.i*yai - s10.i*ybi
	MOVSS X11, X6
	MULSS X13, X6            // s9.i*yai
	MOVSS X9, X7
	MULSS X15, X7            // s10.i*ybi
	SUBSS X7, X6
	MOVSS X6, 8(SP)          // s12.r

	// s12.i = s10.r*ybi - s9.r*yai
	MOVSS X8, X6
	MULSS X15, X6            // s10.r*ybi
	MOVSS X10, X7
	MULSS X13, X7            // s9.r*yai
	SUBSS X7, X6
	MOVSS X6, 12(SP)         // s12.i

	// fout[idx2] = s11 + s12
	MOVSS (SP), X6
	ADDSS 8(SP), X6
	MOVSS 4(SP), X7
	ADDSS 12(SP), X7
	LEAQ (R10)(R10*1), R9    // 2*m*8
	MOVSS X6, (BX)(R9*1)
	MOVSS X7, 4(BX)(R9*1)

	// fout[idx3] = s11 - s12
	MOVSS (SP), X6
	SUBSS 8(SP), X6
	MOVSS 4(SP), X7
	SUBSS 12(SP), X7
	LEAQ (R10)(R10*2), R9    // 3*m*8
	MOVSS X6, (BX)(R9*1)
	MOVSS X7, 4(BX)(R9*1)

	ADDQ $32, SP

	// Advance
	ADDQ $8, BX
	ADDQ R11, R15
	DECQ DX
	JNZ  bfly5_inner

	// Restore m for next outer iteration
	MOVQ m+48(FP), DX
	INCQ AX
	JMP  bfly5_outer

bfly5_done:
	RET

// func kfBfly3Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int)
TEXT ·kfBfly3Inner(SB), NOSPLIT, $0-80
	MOVQ fout_base+0(FP), DI
	MOVQ w_base+24(FP), SI
	MOVQ m+48(FP), DX
	MOVQ N+56(FP), CX
	MOVQ mm+64(FP), R8
	MOVQ fstride+72(FP), R9

	// epi3i = w[fstride*m].i
	MOVQ R9, AX
	IMULQ DX, AX
	SHLQ $3, AX
	MOVSS 4(SI)(AX*1), X12   // epi3i

	// half = 0.5
	MOVL $0x3F000000, AX     // float32 bit pattern for 0.5
	MOVL AX, X13

	// strides
	MOVQ DX, R10
	SHLQ $3, R10             // m*8
	MOVQ R9, R11
	SHLQ $3, R11             // fstride*8
	MOVQ R11, R12
	SHLQ $1, R12             // fstride*2*8
	SHLQ $3, R8              // mm*8

	XORQ AX, AX              // outer i

bfly3_outer:
	CMPQ AX, CX
	JGE  bfly3_done

	MOVQ AX, BX
	IMULQ R8, BX
	LEAQ (DI)(BX*1), BX

	XORQ R15, R15
	MOVQ DX, R9

bfly3_inner:
	// a0
	MOVSS (BX), X0
	MOVSS 4(BX), X1

	// b1, w1
	MOVSS (BX)(R10*1), X2
	MOVSS 4(BX)(R10*1), X3
	MOVSS (SI)(R15*1), X4
	MOVSS 4(SI)(R15*1), X5

	// s1 = cmul(b1, w1)
	MOVSS X2, X6
	MULSS X4, X6
	MOVSS X3, X7
	MULSS X5, X7
	SUBSS X7, X6             // s1.r
	MOVSS X2, X7
	MULSS X5, X7
	MOVSS X3, X8
	MULSS X4, X8
	ADDSS X8, X7             // s1.i

	// b2, w2
	LEAQ (R10)(R10*1), R13
	MOVSS (BX)(R13*1), X2
	MOVSS 4(BX)(R13*1), X3
	MOVQ R15, R13
	SHLQ $1, R13
	MOVSS (SI)(R13*1), X4
	MOVSS 4(SI)(R13*1), X5

	// s2 = cmul(b2, w2)
	MOVSS X2, X8
	MULSS X4, X8
	MOVSS X3, X9
	MULSS X5, X9
	SUBSS X9, X8             // s2.r
	MOVSS X2, X9
	MULSS X5, X9
	MOVSS X3, X10
	MULSS X4, X10
	ADDSS X10, X9            // s2.i

	// s3 = s1 + s2
	MOVSS X6, X2
	ADDSS X8, X2             // s3.r
	MOVSS X7, X3
	ADDSS X9, X3             // s3.i
	// s0 = s1 - s2
	SUBSS X8, X6             // s0.r
	SUBSS X9, X7             // s0.i

	// f1.r = a0.r - 0.5*s3.r (kissHalfSub)
	MOVSS X2, X4
	MULSS X13, X4
	MOVSS X0, X8
	SUBSS X4, X8             // f1.r
	// f1.i = a0.i - 0.5*s3.i
	MOVSS X3, X4
	MULSS X13, X4
	MOVSS X1, X9
	SUBSS X4, X9             // f1.i

	// s0 *= epi3i
	MULSS X12, X6
	MULSS X12, X7

	// fout[idx0] = a0 + s3
	ADDSS X2, X0
	ADDSS X3, X1
	MOVSS X0, (BX)
	MOVSS X1, 4(BX)

	// fout[idx2] = f1 + (s0.i, -s0.r)
	MOVSS X8, X4
	ADDSS X7, X4             // f1.r + s0.i
	MOVSS X9, X5
	SUBSS X6, X5             // f1.i - s0.r
	LEAQ (R10)(R10*1), R13
	MOVSS X4, (BX)(R13*1)
	MOVSS X5, 4(BX)(R13*1)

	// fout[idx1] = f1 - (s0.i, -s0.r)
	SUBSS X7, X8             // f1.r - s0.i
	ADDSS X6, X9             // f1.i + s0.r
	MOVSS X8, (BX)(R10*1)
	MOVSS X9, 4(BX)(R10*1)

	ADDQ $8, BX
	ADDQ R11, R15
	DECQ R9
	JNZ  bfly3_inner

	MOVQ m+48(FP), DX
	INCQ AX
	JMP  bfly3_outer

bfly3_done:
	RET

// func kfBfly4Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int)
TEXT ·kfBfly4Inner(SB), NOSPLIT, $0-80
	MOVQ fout_base+0(FP), DI
	MOVQ w_base+24(FP), SI
	MOVQ m+48(FP), DX
	MOVQ N+56(FP), CX
	MOVQ mm+64(FP), R8
	MOVQ fstride+72(FP), R9

	MOVQ DX, R10
	SHLQ $3, R10             // m*8
	MOVQ R9, R11
	SHLQ $3, R11             // fstride*8
	MOVQ R11, R12
	SHLQ $1, R12             // fstride*2*8
	LEAQ (R11)(R12*1), R13   // fstride*3*8
	SHLQ $3, R8              // mm*8

	XORQ AX, AX

bfly4_outer:
	CMPQ AX, CX
	JGE  bfly4_done

	MOVQ AX, BX
	IMULQ R8, BX
	LEAQ (DI)(BX*1), BX

	XORQ R15, R15
	MOVQ DX, R9

bfly4_inner:
	// f0
	MOVSS (BX), X0
	MOVSS 4(BX), X1

	// b1, w1 → s0
	MOVSS (BX)(R10*1), X2
	MOVSS 4(BX)(R10*1), X3
	MOVSS (SI)(R15*1), X4
	MOVSS 4(SI)(R15*1), X5
	MOVSS X2, X6
	MULSS X4, X6
	MOVSS X3, X7
	MULSS X5, X7
	SUBSS X7, X6             // s0.r
	MOVSS X2, X7
	MULSS X5, X7
	MOVSS X3, X8
	MULSS X4, X8
	ADDSS X8, X7             // s0.i

	// b2, w2 → s1
	LEAQ (R10)(R10*1), R14
	MOVSS (BX)(R14*1), X2
	MOVSS 4(BX)(R14*1), X3
	MOVQ R15, R14
	SHLQ $1, R14
	MOVSS (SI)(R14*1), X4
	MOVSS 4(SI)(R14*1), X5
	MOVSS X2, X8
	MULSS X4, X8
	MOVSS X3, X9
	MULSS X5, X9
	SUBSS X9, X8             // s1.r
	MOVSS X2, X9
	MULSS X5, X9
	MOVSS X3, X10
	MULSS X4, X10
	ADDSS X10, X9            // s1.i

	// b3, w3 → s2
	LEAQ (R10)(R10*2), R14
	MOVSS (BX)(R14*1), X2
	MOVSS 4(BX)(R14*1), X3
	// tw3 = tw1 + fstride2*8
	LEAQ (R15)(R12*1), R14
	ADDQ R11, R14            // fstride3*8... wait, R13 = fstride*3*8.
	// tw3_offset = tw1 + fstride*2*8 = tw1*3? No.
	// tw1 increments by fstride*8, tw3 increments by fstride*3*8
	// tw3 = u * fstride*3*8 = (tw1/fstride*8) * fstride*3*8 = tw1*3
	MOVQ R15, R14
	LEAQ (R14)(R14*2), R14   // tw3 = 3*tw1
	MOVSS (SI)(R14*1), X4
	MOVSS 4(SI)(R14*1), X5
	MOVSS X2, X10
	MULSS X4, X10
	MOVSS X3, X11
	MULSS X5, X11
	SUBSS X11, X10           // s2.r
	MOVSS X2, X11
	MULSS X5, X11
	MOVSS X3, X2
	MULSS X4, X2
	ADDSS X2, X11            // s2.i

	// Butterfly:
	// s5 = f0 - s1
	MOVSS X0, X2
	SUBSS X8, X2             // s5.r
	MOVSS X1, X3
	SUBSS X9, X3             // s5.i
	// f0 += s1
	ADDSS X8, X0
	ADDSS X9, X1
	// s3 = s0 + s2
	MOVSS X6, X4
	ADDSS X10, X4            // s3.r
	MOVSS X7, X5
	ADDSS X11, X5            // s3.i
	// s4 = s0 - s2
	SUBSS X10, X6            // s4.r
	SUBSS X11, X7            // s4.i

	// fout[idx2] = f0 - s3
	MOVSS X0, X8
	SUBSS X4, X8
	MOVSS X1, X9
	SUBSS X5, X9
	LEAQ (R10)(R10*1), R14
	MOVSS X8, (BX)(R14*1)
	MOVSS X9, 4(BX)(R14*1)

	// fout[idx0] = f0 + s3
	ADDSS X4, X0
	ADDSS X5, X1
	MOVSS X0, (BX)
	MOVSS X1, 4(BX)

	// fout[idx1] = s5.r + s4.i, s5.i - s4.r
	MOVSS X2, X8
	ADDSS X7, X8
	MOVSS X3, X9
	SUBSS X6, X9
	MOVSS X8, (BX)(R10*1)
	MOVSS X9, 4(BX)(R10*1)

	// fout[idx3] = s5.r - s4.i, s5.i + s4.r
	SUBSS X7, X2
	ADDSS X6, X3
	LEAQ (R10)(R10*2), R14
	MOVSS X2, (BX)(R14*1)
	MOVSS X3, 4(BX)(R14*1)

	ADDQ $8, BX
	ADDQ R11, R15
	DECQ R9
	JNZ  bfly4_inner

	MOVQ m+48(FP), DX
	INCQ AX
	JMP  bfly4_outer

bfly4_done:
	RET

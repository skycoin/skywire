//go:build arm64 && !purego

#include "textflag.h"

// Vector float ops the Go arm64 assembler lacks mnemonics for (it only exposes
// scalar FADDS/FSUBS/FMULS and the vector FMA VFMLA/VFMLS). Encodings:
//   FMUL Vd.4S, Vn.4S, Vm.4S = 0x6E20DC00 | (Vm<<16) | (Vn<<5) | Vd
//   FADD Vd.4S, Vn.4S, Vm.4S = 0x4E20D400 | (Vm<<16) | (Vn<<5) | Vd
//   FSUB Vd.4S, Vn.4S, Vm.4S = 0x4EA0D400 | (Vm<<16) | (Vn<<5) | Vd

// func haar1Stride1NEON(x []float32, n0 int)
//
// In-place Hadamard butterfly over n0 contiguous (even,odd) float32 pairs,
// stride==1 case of haar1: for each pair (a,b) with c = 1/sqrt(2),
//   out_even = c*a + c*b
//   out_odd  = c*a - c*b
// Each product is a bare FMUL and each combine is a separate FADD/FSUB, the same
// lane math libopus's own NEON kernels use. That is bit-exact with the non-fused
// scalar oracle (purego/amd64); on the quality-gated fused arm64 build the
// scalar reference contracts a*b+c into FMA, so this path is opus_compare-gated
// there rather than byte-identical.
//
// Register map:
//   R0 = x base (advanced by ADD after each 4-pair group and the scalar tail)
//   R1 = n0
//   R2 = vector iteration count (n0/4); R3 = scalar tail count (n0%4)
//   V3 = invSqrt2 broadcast
//   V0 = even lanes, V1 = odd lanes; V4 = c*even, V5 = c*odd
TEXT ·haar1Stride1NEON(SB), NOSPLIT, $0-32
	MOVD x_base+0(FP), R0
	MOVD n0+24(FP), R1

	CBZ  R1, h1_done

	FMOVS $0.70710678118654752440, F3
	VDUP  V3.S[0], V3.S4          // V3 = {c,c,c,c}

	LSR  $2, R1, R2               // R2 = n0/4 (4 pairs per iteration)
	AND  $3, R1, R3               // R3 = n0%4
	CBZ  R2, h1_tail

h1_loop4:
	// Load 4 pairs: V0 = {even0..even3}, V1 = {odd0..odd3}
	VLD2 (R0), [V0.S4, V1.S4]

	WORD $0x6E23DC04             // FMUL V4.4S, V0.4S, V3.4S  (c*even)
	WORD $0x6E23DC25             // FMUL V5.4S, V1.4S, V3.4S  (c*odd)

	WORD $0x4E25D480            // FADD V0.4S, V4.4S, V5.4S  (even_out = c*even + c*odd)
	WORD $0x4EA5D481            // FSUB V1.4S, V4.4S, V5.4S  (odd_out  = c*even - c*odd)

	VST2 [V0.S4, V1.S4], (R0)
	ADD  $32, R0

	SUBS $1, R2
	BNE  h1_loop4

h1_tail:
	CBZ  R3, h1_done

h1_tail1:
	FMOVS (R0), F0               // even (x[2j])
	FMOVS 4(R0), F1              // odd  (x[2j+1])
	FMULS F3, F0, F4            // c*even
	FMULS F3, F1, F5           // c*odd
	FADDS F4, F5, F0           // even_out = c*even + c*odd
	FSUBS F5, F4, F1          // odd_out  = c*even - c*odd
	FMOVS F0, (R0)
	FMOVS F1, 4(R0)
	ADD   $8, R0
	SUBS  $1, R3
	BNE   h1_tail1

h1_done:
	RET

// func haar1Stride2NEON(x []float32, n0 int)
//
// stride==2 case of haar1: the butterfly pairs index i with i+2 (step 4) over
// two outer passes (i in {0,1}), i.e. each 4-element group {a,b,c,d} yields
//   out = {c*a+c*c2, c*b+c*d, c*a-c*c2, c*b-c*d}   (pairs (a,group[2]),(b,group[3]))
// where c = 1/sqrt(2). n0 is the per-outer pair count, so there are n0 such
// 4-element groups. UZP1/UZP2 on .2D gather the low pair-halves into A and the
// high halves into B across two groups (8 elements), and ZIP1/ZIP2 scatter the
// sum/difference back; an odd trailing group is handled with a .2S tail.
//
// Register map:
//   R0 = x base; R1 = n0
//   R2 = 8-element iterations (n0/2); R3 = trailing 4-element group (n0&1)
//   V3 = invSqrt2 broadcast
//   V8 = A (low halves), V9 = B (high halves); V4 = c*A, V5 = c*B
TEXT ·haar1Stride2NEON(SB), NOSPLIT, $0-32
	MOVD x_base+0(FP), R0
	MOVD n0+24(FP), R1

	CBZ  R1, h2_done

	FMOVS $0.70710678118654752440, F3
	VDUP  V3.S[0], V3.S4

	LSR  $1, R1, R2              // R2 = n0/2 (two groups / 8 elems per iter)
	AND  $1, R1, R3             // R3 = n0&1 (one trailing 4-elem group)
	CBZ  R2, h2_tail

h2_body:
	VLD1  (R0), [V0.D2, V1.D2]  // V0={x0..x3} V1={x4..x7}
	VUZP1 V1.D2, V0.D2, V8.D2   // A = {x0,x1,x4,x5}
	VUZP2 V1.D2, V0.D2, V9.D2   // B = {x2,x3,x6,x7}

	WORD $0x6E23DD04           // FMUL V4.4S, V8.4S, V3.4S  (c*A)
	WORD $0x6E23DD25           // FMUL V5.4S, V9.4S, V3.4S  (c*B)
	WORD $0x4E25D486          // FADD V6.4S, V4.4S, V5.4S  (lo = c*A + c*B)
	WORD $0x4EA5D487         // FSUB V7.4S, V4.4S, V5.4S  (hi = c*A - c*B)

	VZIP1 V7.D2, V6.D2, V10.D2  // {out0,out1,out2,out3}
	VZIP2 V7.D2, V6.D2, V11.D2  // {out4,out5,out6,out7}
	VST1  [V10.D2, V11.D2], (R0)
	ADD   $32, R0

	SUBS $1, R2
	BNE  h2_body

h2_tail:
	CBZ  R3, h2_done
	// trailing 4-elem group {x0,x1,x2,x3}: lo={x0,x1}=V0.2S, hi={x2,x3}=V0.D[1]
	VLD1 (R0), [V0.S4]
	VMOV V0.D[1], V1.D[0]
	WORD $0x0E23DC04           // FMUL V4.2S, V0.2S, V3.2S
	WORD $0x0E23DC25           // FMUL V5.2S, V1.2S, V3.2S
	WORD $0x0E25D486          // FADD V6.2S, V4.2S, V5.2S
	WORD $0x0EA5D487         // FSUB V7.2S, V4.2S, V5.2S
	VMOV V6.D[0], V8.D[0]
	VMOV V7.D[0], V8.D[1]
	VST1 [V8.S4], (R0)

h2_done:
	RET

// func haar1Stride4NEON(x []float32, n0 int)
//
// stride==4 case of haar1: each 8-element group {x0..x7} pairs the four low
// lanes {x0,x1,x2,x3} with the four high lanes {x4,x5,x6,x7} (step 8 over four
// outer passes), so it is a direct 4-lane butterfly with no shuffles:
//   lo = c*{x0..x3} + c*{x4..x7}  -> x0..x3
//   hi = c*{x0..x3} - c*{x4..x7}  -> x4..x7
// where c = 1/sqrt(2). n0 is the per-outer pair count = the group count.
//
// Register map:
//   R0 = x base; R1 = n0; R2 = group count
//   V3 = invSqrt2 broadcast; V0 = lows, V1 = highs; V4 = c*lo, V5 = c*hi
TEXT ·haar1Stride4NEON(SB), NOSPLIT, $0-32
	MOVD x_base+0(FP), R0
	MOVD n0+24(FP), R1

	CBZ  R1, h4_done

	FMOVS $0.70710678118654752440, F3
	VDUP  V3.S[0], V3.S4

	MOVD R1, R2

h4_loop:
	VLD1 (R0), [V0.S4, V1.S4]   // V0={x0..x3} V1={x4..x7}
	WORD $0x6E23DC04           // FMUL V4.4S, V0.4S, V3.4S  (c*lo)
	WORD $0x6E23DC25           // FMUL V5.4S, V1.4S, V3.4S  (c*hi)
	WORD $0x4E25D486          // FADD V6.4S, V4.4S, V5.4S  (out lo)
	WORD $0x4EA5D487         // FSUB V7.4S, V4.4S, V5.4S  (out hi)
	VST1 [V6.S4, V7.S4], (R0)
	ADD  $32, R0

	SUBS $1, R2
	BNE  h4_loop

h4_done:
	RET

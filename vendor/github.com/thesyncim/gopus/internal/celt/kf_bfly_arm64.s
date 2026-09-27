//go:build arm64 && !purego
#include "textflag.h"

// BFLY_TW_GATHER loads four kissCpx twiddles spaced Rstep bytes apart from
// Rtw (advancing it) and deinterleaves them into Vr (real lanes) and Vi
// (imaginary lanes) via V14/V15 scratch.
#define BFLY_TW_GATHER(Rtw, Rstep, Vr, Vi) \
	MOVD  (Rtw), R17        \
	VMOV  R17, V14.D[0]     \
	ADD   Rstep, Rtw        \
	MOVD  (Rtw), R17        \
	VMOV  R17, V14.D[1]     \
	ADD   Rstep, Rtw        \
	MOVD  (Rtw), R17        \
	VMOV  R17, V15.D[0]     \
	ADD   Rstep, Rtw        \
	MOVD  (Rtw), R17        \
	VMOV  R17, V15.D[1]     \
	ADD   Rstep, Rtw        \
	VUZP1 V15.S4, V14.S4, Vr.S4 \
	VUZP2 V15.S4, V14.S4, Vi.S4


// All three butterfly inner loops for ARM64.
// On ARM64, kissFFTFMALikeEnabled=true, so complex twiddle multiply uses:
//   kissMulSubSource(a,b,c,d) = round(a*b - round(c*d))  → FMULS + FNEGS + FMADDS
//   kissMulAddSource(a,b,c,d) = round(a*b + round(c*d))  → FMULS + FMADDS
// kissCpx is {r float32, i float32} = 8 bytes, no padding.

// kfBfly5Inner implements the radix-5 butterfly inner loop. The main loop
// computes four u-steps per NEON iteration: legs load contiguously, the four
// twiddle streams gather at their fstride spacing, ya/yb sit broadcast in
// V28-V31, and every lane runs the exact scalar op sequence (FMA-like cmul
// and kissMulAddSource/kissMulSubSource recombines), so results are
// bit-identical per element. A scalar loop finishes m%4.
TEXT ·kfBfly5Inner(SB), NOSPLIT, $0-80
	MOVD fout_base+0(FP), R0
	MOVD w_base+24(FP), R1
	MOVD m+48(FP), R2
	MOVD N+56(FP), R3
	MOVD mm+64(FP), R4
	MOVD fstride+72(FP), R5

	CMP  $1, R3
	BLT  bfly5_done
	CMP  $1, R2
	BLT  bfly5_done

	// ya = w[fstride*m], yb = w[fstride*2*m], broadcast to V28..V31.
	MUL   R5, R2, R6
	LSL   $3, R6, R6
	ADD   R1, R6, R7
	FMOVS (R7), F28
	FMOVS 4(R7), F29
	ADD   R6, R7, R7
	FMOVS (R7), F30
	FMOVS 4(R7), F31
	VDUP  V28.S[0], V28.S4
	VDUP  V29.S[0], V29.S4
	VDUP  V30.S[0], V30.S4
	VDUP  V31.S[0], V31.S4

	LSL  $3, R5, R6   // fstride*8
	LSL  $1, R6, R26  // fstride*16
	ADD  R6, R26, R25 // fstride*24
	LSL  $1, R26, R24 // fstride*32
	LSL  $3, R2, R21  // m*8
	LSL  $3, R4, R19  // mm*8
	LSR  $2, R2, R22  // vector blocks
	AND  $3, R2, R23  // scalar tail
	MOVD R0, R20
	MOVD R3, R16

bfly5_outer:
	MOVD R20, R7
	ADD  R21, R7, R8
	ADD  R21, R8, R9
	ADD  R21, R9, R10
	ADD  R21, R10, R11
	MOVD R1, R12 // tw1
	MOVD R1, R13 // tw2
	MOVD R1, R4  // tw3
	MOVD R1, R2  // tw4

	MOVD R22, R14
	CBZ  R14, bfly5_scalar_setup

bfly5_vec:
	VLD2 (R7), [V0.S4, V1.S4]  // s0 r/i
	VLD2 (R8), [V2.S4, V3.S4]  // b1
	VLD2 (R9), [V4.S4, V5.S4]  // b2
	VLD2 (R10), [V6.S4, V7.S4] // b3
	VLD2 (R11), [V8.S4, V9.S4] // b4
	BFLY_TW_GATHER(R12, R6, V16, V17)
	BFLY_TW_GATHER(R13, R26, V18, V19)
	BFLY_TW_GATHER(R4, R25, V20, V21)
	BFLY_TW_GATHER(R2, R24, V22, V23)

	WORD $0x6E31DC78 // FMUL V24,V3,V17 (b1i*w1i)
	WORD $0x6EA0FB18 // FNEG V24
	VFMLA V16.S4, V2.S4, V24.S4 // s1r
	WORD $0x6E30DC79 // FMUL V25,V3,V16 (b1i*w1r)
	VFMLA V17.S4, V2.S4, V25.S4 // s1i
	WORD $0x6E33DCBA // FMUL V26,V5,V19
	WORD $0x6EA0FB5A // FNEG V26
	VFMLA V18.S4, V4.S4, V26.S4 // s2r
	WORD $0x6E32DCBB // FMUL V27,V5,V18
	VFMLA V19.S4, V4.S4, V27.S4 // s2i
	WORD $0x6E35DCE2 // FMUL V2,V7,V21
	WORD $0x6EA0F842 // FNEG V2
	VFMLA V20.S4, V6.S4, V2.S4 // s3r
	WORD $0x6E34DCE3 // FMUL V3,V7,V20
	VFMLA V21.S4, V6.S4, V3.S4 // s3i
	WORD $0x6E37DD24 // FMUL V4,V9,V23
	WORD $0x6EA0F884 // FNEG V4
	VFMLA V22.S4, V8.S4, V4.S4 // s4r
	WORD $0x6E36DD25 // FMUL V5,V9,V22
	VFMLA V23.S4, V8.S4, V5.S4 // s4i
	WORD $0x4E24D706 // FADD V6,V24,V4 s7r
	WORD $0x4E25D727 // FADD V7,V25,V5 s7i
	WORD $0x4EA4D708 // FSUB V8,V24,V4 s10r
	WORD $0x4EA5D729 // FSUB V9,V25,V5 s10i
	WORD $0x4E22D74A // FADD V10,V26,V2 s8r
	WORD $0x4E23D76B // FADD V11,V27,V3 s8i
	WORD $0x4EA2D74C // FSUB V12,V26,V2 s9r
	WORD $0x4EA3D76D // FSUB V13,V27,V3 s9i
	WORD $0x4E2AD4D8 // FADD V24,V6,V10 (s7r+s8r)
	WORD $0x4E2BD4F9 // FADD V25,V7,V11
	WORD $0x4E38D41A // FADD V26,V0,V24 out0r
	WORD $0x4E39D43B // FADD V27,V1,V25 out0i
	VZIP1 V27.S4, V26.S4, V14.S4
	VZIP2 V27.S4, V26.S4, V15.S4
	VST1.P [V14.S4, V15.S4], 32(R7) // out0
	WORD $0x6E3EDD58 // FMUL V24,V10,V30 (s8r*ybr)
	VFMLA V28.S4, V6.S4, V24.S4 // (+s7r*yar)
	WORD $0x4E38D418 // FADD V24,V0,V24 s5r
	WORD $0x6E3EDD79 // FMUL V25,V11,V30
	VFMLA V28.S4, V7.S4, V25.S4 // 
	WORD $0x4E39D439 // FADD V25,V1,V25 s5i
	WORD $0x6E3FDDBA // FMUL V26b,V13,V31 (s9i*ybi)
	VFMLA V29.S4, V9.S4, V26.S4 // s6r
	WORD $0x6E3FDD9B // FMUL V27b,V12,V31 (s9r*ybi)
	VFMLA V29.S4, V8.S4, V27.S4 // 
	WORD $0x6EA0FB7B // FNEG V27 s6i
	WORD $0x4EBAD702 // FSUB V2,V24,V26 out1r
	WORD $0x4EBBD723 // FSUB V3,V25,V27 out1i
	WORD $0x4E3AD704 // FADD V4,V24,V26 out4r
	WORD $0x4E3BD725 // FADD V5,V25,V27 out4i
	VZIP1 V3.S4, V2.S4, V14.S4
	VZIP2 V3.S4, V2.S4, V15.S4
	VST1.P [V14.S4, V15.S4], 32(R8) // out1
	VZIP1 V5.S4, V4.S4, V14.S4
	VZIP2 V5.S4, V4.S4, V15.S4
	VST1.P [V14.S4, V15.S4], 32(R11) // out4
	WORD $0x6E3CDD58 // FMUL V24,V10,V28 (s8r*yar)
	VFMLA V30.S4, V6.S4, V24.S4 // (+s7r*ybr)
	WORD $0x4E38D418 // FADD V24,V0,V24 s11r
	WORD $0x6E3CDD79 // FMUL V25,V11,V28
	VFMLA V30.S4, V7.S4, V25.S4 // 
	WORD $0x4E39D439 // FADD V25,V1,V25 s11i
	WORD $0x6E3FDD3A // FMUL V26,V9,V31 (s10i*ybi)
	WORD $0x6EA0FB5A // FNEG V26
	VFMLA V29.S4, V13.S4, V26.S4 // s12r
	WORD $0x6E3DDD9B // FMUL V27,V12,V29 (s9r*yai)
	WORD $0x6EA0FB7B // FNEG V27
	VFMLA V31.S4, V8.S4, V27.S4 // s12i
	WORD $0x4E3AD702 // FADD V2,V24,V26 out2r
	WORD $0x4E3BD723 // FADD V3,V25,V27 out2i
	WORD $0x4EBAD704 // FSUB V4,V24,V26 out3r
	WORD $0x4EBBD725 // FSUB V5,V25,V27 out3i
	VZIP1 V3.S4, V2.S4, V14.S4
	VZIP2 V3.S4, V2.S4, V15.S4
	VST1.P [V14.S4, V15.S4], 32(R9) // out2
	VZIP1 V5.S4, V4.S4, V14.S4
	VZIP2 V5.S4, V4.S4, V15.S4
	VST1.P [V14.S4, V15.S4], 32(R10) // out3

	SUBS $1, R14
	BNE  bfly5_vec

bfly5_scalar_setup:
	MOVD R23, R15
	CBZ  R15, bfly5_next_outer

bfly5_scalar:
	FMOVS (R7), F0  // s0r
	FMOVS 4(R7), F1 // s0i

	FMOVS (R8), F18
	FMOVS 4(R8), F19
	FMOVS (R12), F20
	FMOVS 4(R12), F21
	FMULS  F21, F19, F2
	FNEGS  F2, F2
	FMADDS F20, F2, F18, F2 // s1r
	FMULS  F20, F19, F3
	FMADDS F21, F3, F18, F3 // s1i

	FMOVS (R9), F18
	FMOVS 4(R9), F19
	FMOVS (R13), F20
	FMOVS 4(R13), F21
	FMULS  F21, F19, F4
	FNEGS  F4, F4
	FMADDS F20, F4, F18, F4 // s2r
	FMULS  F20, F19, F5
	FMADDS F21, F5, F18, F5 // s2i

	FMOVS (R10), F18
	FMOVS 4(R10), F19
	FMOVS (R4), F20
	FMOVS 4(R4), F21
	FMULS  F21, F19, F6
	FNEGS  F6, F6
	FMADDS F20, F6, F18, F6 // s3r
	FMULS  F20, F19, F7
	FMADDS F21, F7, F18, F7 // s3i

	FMOVS (R11), F18
	FMOVS 4(R11), F19
	FMOVS (R2), F20
	FMOVS 4(R2), F21
	FMULS  F21, F19, F8
	FNEGS  F8, F8
	FMADDS F20, F8, F18, F8 // s4r
	FMULS  F20, F19, F9
	FMADDS F21, F9, F18, F9 // s4i

	FADDS F8, F2, F10 // s7r
	FADDS F9, F3, F11 // s7i
	FSUBS F8, F2, F12 // s10r
	FSUBS F9, F3, F13 // s10i
	FADDS F6, F4, F14 // s8r
	FADDS F7, F5, F15 // s8i
	FSUBS F6, F4, F16 // s9r
	FSUBS F7, F5, F17 // s9i

	// out0 = s0 + (s7 + s8)
	FADDS F14, F10, F18
	FADDS F15, F11, F19
	FADDS F18, F0, F18
	FADDS F19, F1, F19
	FMOVS F18, (R7)
	FMOVS F19, 4(R7)

	// s5 = s0 + MulAdd(s7, yar, s8, ybr)
	FMULS  F30, F14, F18
	FMADDS F28, F18, F10, F18
	FADDS  F18, F0, F18 // s5r
	FMULS  F30, F15, F19
	FMADDS F28, F19, F11, F19
	FADDS  F19, F1, F19 // s5i

	// s6r = MulAdd(s10i, yai, s9i, ybi); s6i = -MulAdd(s10r, yai, s9r, ybi)
	FMULS  F31, F17, F20
	FMADDS F29, F20, F13, F20 // s6r
	FMULS  F31, F16, F21
	FMADDS F29, F21, F12, F21
	FNEGS  F21, F21 // s6i

	FSUBS F20, F18, F22 // out1r
	FSUBS F21, F19, F23 // out1i
	FMOVS F22, (R8)
	FMOVS F23, 4(R8)
	FADDS F20, F18, F22 // out4r
	FADDS F21, F19, F23 // out4i
	FMOVS F22, (R11)
	FMOVS F23, 4(R11)

	// s11 = s0 + MulAdd(s7, ybr, s8, yar)
	FMULS  F28, F14, F18
	FMADDS F30, F18, F10, F18
	FADDS  F18, F0, F18 // s11r
	FMULS  F28, F15, F19
	FMADDS F30, F19, F11, F19
	FADDS  F19, F1, F19 // s11i

	// s12r = MulSub(s9i, yai, s10i, ybi); s12i = MulSub(s10r, ybi, s9r, yai)
	FMULS  F31, F13, F20
	FNEGS  F20, F20
	FMADDS F29, F20, F17, F20 // s12r
	FMULS  F29, F16, F21
	FNEGS  F21, F21
	FMADDS F31, F21, F12, F21 // s12i

	FADDS F20, F18, F22 // out2r
	FADDS F21, F19, F23 // out2i
	FMOVS F22, (R9)
	FMOVS F23, 4(R9)
	FSUBS F20, F18, F22 // out3r
	FSUBS F21, F19, F23 // out3i
	FMOVS F22, (R10)
	FMOVS F23, 4(R10)

	ADD  $8, R7, R7
	ADD  $8, R8, R8
	ADD  $8, R9, R9
	ADD  $8, R10, R10
	ADD  $8, R11, R11
	ADD  R6, R12, R12
	ADD  R26, R13, R13
	ADD  R25, R4, R4
	ADD  R24, R2, R2

	SUBS $1, R15
	BNE  bfly5_scalar

bfly5_next_outer:
	ADD  R19, R20, R20
	SUBS $1, R16
	BNE  bfly5_outer

bfly5_done:
	RET

// kfBfly3Inner implements the radix-3 butterfly inner loop. The main loop
// computes four j-steps per NEON iteration with the same gather/deinterleave
// scheme as kfBfly4Inner; each lane runs the exact scalar op sequence
// (FMA-like cmul, rounded kissHalfSub/kissScaleMul multiplies, plain
// add/sub combines), so results are bit-identical per element. A scalar
// loop finishes m%4.
TEXT ·kfBfly3Inner(SB), NOSPLIT, $0-80
	MOVD fout_base+0(FP), R0
	MOVD w_base+24(FP), R1
	MOVD m+48(FP), R2
	MOVD N+56(FP), R3
	MOVD mm+64(FP), R4
	MOVD fstride+72(FP), R5

	CMP  $1, R3
	BLT  bfly3_done
	CMP  $1, R2
	BLT  bfly3_done

	// epi3i = w[fstride*m].i, broadcast; 0.5 broadcast.
	MUL   R5, R2, R6
	LSL   $3, R6, R6
	ADD   R1, R6, R6
	FMOVS 4(R6), F30
	VDUP  V30.S[0], V30.S4
	FMOVS $0.5, F31
	VDUP  V31.S[0], V31.S4

	LSL  $3, R5, R6   // fstride*8
	LSL  $1, R6, R26  // fstride*16
	LSL  $3, R2, R21  // m*8
	LSL  $3, R4, R19  // mm*8
	LSR  $2, R2, R22  // vector blocks
	AND  $3, R2, R23  // scalar tail
	MOVD R0, R20
	MOVD R3, R16

bfly3_outer:
	MOVD R20, R7
	ADD  R21, R7, R8
	ADD  R21, R8, R9
	MOVD R1, R11 // tw1
	MOVD R1, R12 // tw2

	MOVD R22, R14
	CBZ  R14, bfly3_scalar_setup

bfly3_vec:
	VLD2 (R7), [V0.S4, V1.S4] // a0 r/i
	VLD2 (R8), [V2.S4, V3.S4] // b1
	VLD2 (R9), [V4.S4, V5.S4] // b2
	BFLY_TW_GATHER(R11, R6, V16, V17)
	BFLY_TW_GATHER(R12, R26, V18, V19)

	WORD $0x6E31DC76           // FMUL V22, V3, V17 (b1i*w1i, rounded)
	WORD $0x6EA0FAD6           // FNEG V22
	VFMLA V16.S4, V2.S4, V22.S4 // s1r
	WORD $0x6E30DC77           // FMUL V23, V3, V16 (b1i*w1r, rounded)
	VFMLA V17.S4, V2.S4, V23.S4 // s1i
	WORD $0x6E33DCB8           // FMUL V24, V5, V19 (b2i*w2i, rounded)
	WORD $0x6EA0FB18           // FNEG V24
	VFMLA V18.S4, V4.S4, V24.S4 // s2r
	WORD $0x6E32DCB9           // FMUL V25, V5, V18 (b2i*w2r, rounded)
	VFMLA V19.S4, V4.S4, V25.S4 // s2i

	WORD $0x4E38D6C8 // FADD V8, V22, V24 (s3r)
	WORD $0x4E39D6E9 // FADD V9, V23, V25 (s3i)
	WORD $0x4EB8D6CA // FSUB V10, V22, V24 (s0r)
	WORD $0x4EB9D6EB // FSUB V11, V23, V25 (s0i)
	WORD $0x6E3FDD0C // FMUL V12, V8, V31 (0.5*s3r, rounded)
	WORD $0x4EACD40C // FSUB V12, V0, V12 (f1r)
	WORD $0x6E3FDD2D // FMUL V13, V9, V31 (0.5*s3i, rounded)
	WORD $0x4EADD42D // FSUB V13, V1, V13 (f1i)
	WORD $0x6E3EDD4A // FMUL V10, V10, V30 (s0r*epi3i, rounded)
	WORD $0x6E3EDD6B // FMUL V11, V11, V30 (s0i*epi3i, rounded)
	WORD $0x4E28D400 // FADD V0, V0, V8 (out0r)
	WORD $0x4E29D421 // FADD V1, V1, V9 (out0i)
	WORD $0x4E2BD596 // FADD V22, V12, V11 (out2r = f1r + s0i)
	WORD $0x4EAAD5B7 // FSUB V23, V13, V10 (out2i = f1i - s0r)
	WORD $0x4EABD598 // FSUB V24, V12, V11 (out1r = f1r - s0i)
	WORD $0x4E2AD5B9 // FADD V25, V13, V10 (out1i = f1i + s0r)

	VZIP1 V1.S4, V0.S4, V14.S4
	VZIP2 V1.S4, V0.S4, V15.S4
	VST1.P [V14.S4, V15.S4], 32(R7)
	VZIP1 V23.S4, V22.S4, V14.S4
	VZIP2 V23.S4, V22.S4, V15.S4
	VST1.P [V14.S4, V15.S4], 32(R9)
	VZIP1 V25.S4, V24.S4, V14.S4
	VZIP2 V25.S4, V24.S4, V15.S4
	VST1.P [V14.S4, V15.S4], 32(R8)

	SUBS $1, R14
	BNE  bfly3_vec

bfly3_scalar_setup:
	MOVD R23, R15
	CBZ  R15, bfly3_next_outer

bfly3_scalar:
	FMOVS (R7), F0   // a0r
	FMOVS 4(R7), F1  // a0i
	FMOVS (R8), F2   // b1r
	FMOVS 4(R8), F3  // b1i
	FMOVS (R11), F16 // w1r
	FMOVS 4(R11), F17

	FMULS  F17, F3, F4
	FNEGS  F4, F4
	FMADDS F16, F4, F2, F4 // s1r
	FMULS  F16, F3, F5
	FMADDS F17, F5, F2, F5 // s1i

	FMOVS (R9), F6
	FMOVS 4(R9), F7
	FMOVS (R12), F18
	FMOVS 4(R12), F19

	FMULS  F19, F7, F8
	FNEGS  F8, F8
	FMADDS F18, F8, F6, F8 // s2r
	FMULS  F18, F7, F9
	FMADDS F19, F9, F6, F9 // s2i

	FADDS F8, F4, F10 // s3r
	FADDS F9, F5, F11 // s3i
	FSUBS F8, F4, F12 // s0r
	FSUBS F9, F5, F13 // s0i

	FMULS F31, F10, F14 // 0.5*s3r
	FSUBS F14, F0, F14  // f1r
	FMULS F31, F11, F15 // 0.5*s3i
	FSUBS F15, F1, F15  // f1i
	FMULS F30, F12, F12 // s0r*epi3i
	FMULS F30, F13, F13 // s0i*epi3i

	FADDS F10, F0, F0 // out0r
	FADDS F11, F1, F1
	FMOVS F0, (R7)
	FMOVS F1, 4(R7)

	FADDS F13, F14, F2 // out2r = f1r + s0i
	FSUBS F12, F15, F3 // out2i = f1i - s0r
	FMOVS F2, (R9)
	FMOVS F3, 4(R9)

	FSUBS F13, F14, F2 // out1r
	FADDS F12, F15, F3 // out1i
	FMOVS F2, (R8)
	FMOVS F3, 4(R8)

	ADD  $8, R7, R7
	ADD  $8, R8, R8
	ADD  $8, R9, R9
	ADD  R6, R11, R11
	ADD  R26, R12, R12

	SUBS $1, R15
	BNE  bfly3_scalar

bfly3_next_outer:
	ADD  R19, R20, R20
	SUBS $1, R16
	BNE  bfly3_outer

bfly3_done:
	RET

// kfBfly4Inner implements the radix-4 butterfly inner loop. The main loop
// computes four j-steps per NEON iteration: the four legs load contiguously
// (VLD2 r/i deinterleave), the three twiddle streams gather lane-by-lane at
// their fstride spacing (UZP1/UZP2 split r/i), and every lane runs the exact
// scalar op sequence — kissMulSubSource/kissMulAddSource as rounded FMUL,
// FNEG, fused FMLA, and plain FADD/FSUB combines — so results are
// bit-identical per element. A scalar loop finishes m%4.
TEXT ·kfBfly4Inner(SB), NOSPLIT, $0-80
	MOVD fout_base+0(FP), R0
	MOVD w_base+24(FP), R1
	MOVD m+48(FP), R2
	MOVD N+56(FP), R3
	MOVD mm+64(FP), R4
	MOVD fstride+72(FP), R5

	CMP  $1, R3
	BLT  bfly4_done
	CMP  $1, R2
	BLT  bfly4_done

	LSL  $3, R5, R6   // fstride*8
	LSL  $1, R6, R26  // fstride*16
	ADD  R6, R26, R25 // fstride*24
	LSL  $3, R2, R21  // m*8
	LSL  $3, R4, R19  // mm*8
	LSR  $2, R2, R22  // vector blocks per inner loop
	AND  $3, R2, R23  // scalar tail per inner loop
	MOVD R0, R20      // &fout[i*mm]
	MOVD R3, R16      // outer counter

bfly4_outer:
	MOVD R20, R7
	ADD  R21, R7, R8
	ADD  R21, R8, R9
	ADD  R21, R9, R10
	MOVD R1, R11 // tw1
	MOVD R1, R12 // tw2
	MOVD R1, R13 // tw3

	MOVD R22, R14
	CBZ  R14, bfly4_scalar_setup

bfly4_vec:
	VLD2 (R7), [V0.S4, V1.S4]  // f0 r/i
	VLD2 (R8), [V2.S4, V3.S4]  // b1
	VLD2 (R9), [V4.S4, V5.S4]  // b2
	VLD2 (R10), [V6.S4, V7.S4] // b3
	BFLY_TW_GATHER(R11, R6, V16, V17)
	BFLY_TW_GATHER(R12, R26, V18, V19)
	BFLY_TW_GATHER(R13, R25, V20, V21)

	WORD $0x6E31DC76           // FMUL V22, V3, V17 (b1i*w1i, rounded)
	WORD $0x6EA0FAD6           // FNEG V22
	VFMLA V16.S4, V2.S4, V22.S4 // s0r = -(b1i*w1i) + b1r*w1r
	WORD $0x6E30DC77           // FMUL V23, V3, V16 (b1i*w1r, rounded)
	VFMLA V17.S4, V2.S4, V23.S4 // s0i = b1i*w1r + b1r*w1i
	WORD $0x6E33DCB8           // FMUL V24, V5, V19 (b2i*w2i, rounded)
	WORD $0x6EA0FB18           // FNEG V24
	VFMLA V18.S4, V4.S4, V24.S4 // s1r
	WORD $0x6E32DCB9           // FMUL V25, V5, V18 (b2i*w2r, rounded)
	VFMLA V19.S4, V4.S4, V25.S4 // s1i
	WORD $0x6E35DCFA           // FMUL V26, V7, V21 (b3i*w3i, rounded)
	WORD $0x6EA0FB5A           // FNEG V26
	VFMLA V20.S4, V6.S4, V26.S4 // s2r
	WORD $0x6E34DCFB           // FMUL V27, V7, V20 (b3i*w3r, rounded)
	VFMLA V21.S4, V6.S4, V27.S4 // s2i

	WORD $0x4EB8D41C // FSUB V28, V0, V24 (s5r = f0r - s1r)
	WORD $0x4EB9D43D // FSUB V29, V1, V25 (s5i)
	WORD $0x4E38D400 // FADD V0, V0, V24 (f0r += s1r)
	WORD $0x4E39D421 // FADD V1, V1, V25 (f0i += s1i)
	WORD $0x4E3AD6C8 // FADD V8, V22, V26 (s3r)
	WORD $0x4E3BD6E9 // FADD V9, V23, V27 (s3i)
	WORD $0x4EBAD6CA // FSUB V10, V22, V26 (s4r)
	WORD $0x4EBBD6EB // FSUB V11, V23, V27 (s4i)
	WORD $0x4EA8D40C // FSUB V12, V0, V8 (out2r)
	WORD $0x4EA9D42D // FSUB V13, V1, V9 (out2i)
	WORD $0x4E28D400 // FADD V0, V0, V8 (out0r)
	WORD $0x4E29D421 // FADD V1, V1, V9 (out0i)
	WORD $0x4E2BD796 // FADD V22, V28, V11 (out1r = s5r + s4i)
	WORD $0x4EAAD7B7 // FSUB V23, V29, V10 (out1i = s5i - s4r)
	WORD $0x4EABD798 // FSUB V24, V28, V11 (out3r = s5r - s4i)
	WORD $0x4E2AD7B9 // FADD V25, V29, V10 (out3i = s5i + s4r)

	VZIP1 V13.S4, V12.S4, V14.S4
	VZIP2 V13.S4, V12.S4, V15.S4
	VST1.P [V14.S4, V15.S4], 32(R9)
	VZIP1 V1.S4, V0.S4, V14.S4
	VZIP2 V1.S4, V0.S4, V15.S4
	VST1.P [V14.S4, V15.S4], 32(R7)
	VZIP1 V23.S4, V22.S4, V14.S4
	VZIP2 V23.S4, V22.S4, V15.S4
	VST1.P [V14.S4, V15.S4], 32(R8)
	VZIP1 V25.S4, V24.S4, V14.S4
	VZIP2 V25.S4, V24.S4, V15.S4
	VST1.P [V14.S4, V15.S4], 32(R10)

	SUBS $1, R14
	BNE  bfly4_vec

bfly4_scalar_setup:
	MOVD R23, R15
	CBZ  R15, bfly4_next_outer

bfly4_scalar:
	FMOVS (R7), F0   // f0r
	FMOVS 4(R7), F1  // f0i
	FMOVS (R8), F2   // b1r
	FMOVS 4(R8), F3  // b1i
	FMOVS (R11), F16 // w1r
	FMOVS 4(R11), F17

	FMULS  F17, F3, F4
	FNEGS  F4, F4
	FMADDS F16, F4, F2, F4 // s0r
	FMULS  F16, F3, F5
	FMADDS F17, F5, F2, F5 // s0i

	FMOVS (R9), F6
	FMOVS 4(R9), F7
	FMOVS (R12), F18
	FMOVS 4(R12), F19

	FMULS  F19, F7, F8
	FNEGS  F8, F8
	FMADDS F18, F8, F6, F8 // s1r
	FMULS  F18, F7, F9
	FMADDS F19, F9, F6, F9 // s1i

	FMOVS (R10), F10
	FMOVS 4(R10), F11
	FMOVS (R13), F20
	FMOVS 4(R13), F21

	FMULS  F21, F11, F12
	FNEGS  F12, F12
	FMADDS F20, F12, F10, F12 // s2r
	FMULS  F20, F11, F13
	FMADDS F21, F13, F10, F13 // s2i

	FSUBS F8, F0, F14 // s5r
	FSUBS F9, F1, F15 // s5i
	FADDS F8, F0, F0  // f0r += s1r
	FADDS F9, F1, F1
	FADDS F12, F4, F22 // s3r
	FADDS F13, F5, F23 // s3i
	FSUBS F12, F4, F24 // s4r
	FSUBS F13, F5, F25 // s4i

	FSUBS F22, F0, F26 // out2r
	FSUBS F23, F1, F27
	FMOVS F26, (R9)
	FMOVS F27, 4(R9)

	FADDS F22, F0, F0 // out0
	FADDS F23, F1, F1
	FMOVS F0, (R7)
	FMOVS F1, 4(R7)

	FADDS F25, F14, F2 // out1r = s5r + s4i
	FSUBS F24, F15, F3 // out1i = s5i - s4r
	FMOVS F2, (R8)
	FMOVS F3, 4(R8)

	FSUBS F25, F14, F2 // out3r
	FADDS F24, F15, F3 // out3i
	FMOVS F2, (R10)
	FMOVS F3, 4(R10)

	ADD  $8, R7, R7
	ADD  $8, R8, R8
	ADD  $8, R9, R9
	ADD  $8, R10, R10
	ADD  R6, R11, R11
	ADD  R26, R12, R12
	ADD  R25, R13, R13

	SUBS $1, R15
	BNE  bfly4_scalar

bfly4_next_outer:
	ADD  R19, R20, R20
	SUBS $1, R16
	BNE  bfly4_outer

bfly4_done:
	RET

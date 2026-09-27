//go:build arm64 && !purego

#include "textflag.h"

// Shared twiddle/scale/scatter tail for the fold kernels, applied to V20 (re)
// and V21 (im) for outputs i0+4b..i0+4b+3:
//
//	yr = fma(re, t0, -(im*t1)); yi = fma(im, t0, re*t1)
//	dst[bitrev[i]] = kissCpx{yr*preScale, yi*preScale}
//
// Register conventions for both kernels:
//	R0 dst base, R1 &bitrev[i0], R4 &trig[i0], R5 &trig[n4+i0],
//	R6 blocks, V31 preScale (all lanes).

#define MDCT_STORE_STAGE \
	VLD1.P 16(R4), [V4.S4]    \ // t0
	VLD1.P 16(R5), [V5.S4]    \ // t1
	WORD $0x6E25DEA6          \ // FMUL V6.4S, V21.4S, V5.4S (im*t1)
	WORD $0x6EA0F8C6          \ // FNEG V6.4S, V6.4S
	VFMLA V4.S4, V20.S4, V6.S4 \ // yr = -(im*t1) + re*t0
	WORD $0x6E25DE87          \ // FMUL V7.4S, V20.4S, V5.4S (re*t1)
	VFMLA V4.S4, V21.S4, V7.S4 \ // yi = re*t1 + im*t0
	WORD $0x6E3FDCC6          \ // FMUL V6.4S, V6.4S, V31.4S
	WORD $0x6E3FDCE7          \ // FMUL V7.4S, V7.4S, V31.4S
	VZIP1 V7.S4, V6.S4, V8.S4 \
	VZIP2 V7.S4, V6.S4, V9.S4 \
	MOVD.P 8(R1), R7          \
	ADD R7<<3, R0, R8         \
	VMOV V8.D[0], R9          \
	MOVD R9, (R8)             \
	MOVD.P 8(R1), R7          \
	ADD R7<<3, R0, R8         \
	VMOV V8.D[1], R9          \
	MOVD R9, (R8)             \
	MOVD.P 8(R1), R7          \
	ADD R7<<3, R0, R8         \
	VMOV V9.D[0], R9          \
	MOVD R9, (R8)             \
	MOVD.P 8(R1), R7          \
	ADD R7<<3, R0, R8         \
	VMOV V9.D[1], R9          \
	MOVD R9, (R8)

// Loads the descending even-lane stream at (Rsrc) into Vd and steps Rsrc back
// one block: memory holds s[base..base+7]; even lanes reversed give
// s[base+6], s[base+4], s[base+2], s[base].
#define LOAD_DESC(Rsrc, Vd, Vd2) \
	VLD2 (Rsrc), [Vd.S4, Vd2.S4] \
	SUB $32, Rsrc                \
	VREV64 Vd.S4, Vd.S4          \
	VEXT $8, Vd.B16, Vd.B16, Vd.B16

// func mdctFold1StoreNeon(dst []kissCpx, bitrev []int, samples []float32, window []float32, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, preScale float32)
//
// Vectorizes the leading windowed fold of the forward MDCT
// (mdctUseFMALikeMixEnabled path). Per output i = i0+j:
//
//	re = fma(s[xp1+n2+2j], w[wp2-2j], round(s[xp2-2j]*w[wp1+2j]))
//	im = fma(s[xp1+2j],    w[wp1+2j], -round(s[xp2-n2-2j]*w[wp2-2j]))
//
// followed by the common store stage. Bit-identical per element to the scalar
// mdctMulAddMix/mdctMulSubMix + mdctStoreDirectStageFMALike sequence.
TEXT ·mdctFold1StoreNeon(SB), NOSPLIT, $0-188
	MOVD  dst_base+0(FP), R0
	MOVD  bitrev_base+24(FP), R1
	MOVD  samples_base+48(FP), R2
	MOVD  window_base+72(FP), R3
	MOVD  trig_base+96(FP), R4
	MOVD  i0+120(FP), R10
	MOVD  n4+128(FP), R11
	MOVD  n2+136(FP), R12
	MOVD  xp1+144(FP), R13
	MOVD  xp2+152(FP), R14
	MOVD  wp1+160(FP), R15
	MOVD  wp2+168(FP), R16
	MOVD  blocks+176(FP), R6
	FMOVS preScale+184(FP), F31
	VDUP  V31.S[0], V31.S4

	ADD R10<<3, R1, R1  // &bitrev[i0]
	ADD R10<<2, R4, R4  // &trig[i0]
	ADD R11<<2, R4, R5  // &trig[n4+i0]

	// Ascending streams.
	ADD R13<<2, R2, R17 // &s[xp1]
	ADD R12<<2, R17, R19 // &s[xp1+n2]
	ADD R15<<2, R3, R20 // &w[wp1]
	// Descending stream block bases (lowest index of the first block).
	SUB $6, R14, R21
	ADD R21<<2, R2, R21 // &s[xp2-6]
	SUB R12<<2, R21, R22 // &s[xp2-n2-6]
	SUB $6, R16, R23
	ADD R23<<2, R3, R23 // &w[wp2-6]

	CBZ R6, fold1done

fold1loop:
	VLD2.P 32(R19), [V10.S4, V11.S4] // A  = s[xp1+n2+2j]
	VLD2.P 32(R17), [V12.S4, V13.S4] // A2 = s[xp1+2j]
	VLD2.P 32(R20), [V14.S4, V15.S4] // wC = w[wp1+2j]
	LOAD_DESC(R21, V16, V17)         // B  = s[xp2-2j]
	LOAD_DESC(R22, V24, V25)         // B2 = s[xp2-n2-2j]
	LOAD_DESC(R23, V26, V27)         // wD = w[wp2-2j]

	WORD $0x6E2EDE14           // FMUL V20.4S, V16.4S, V14.4S (B*wC, rounded)
	VFMLA V26.S4, V10.S4, V20.S4 // re = round(B*wC) + A*wD
	WORD $0x6E3ADF15           // FMUL V21.4S, V24.4S, V26.4S (B2*wD, rounded)
	WORD $0x6EA0FAB5           // FNEG V21.4S, V21.4S
	VFMLA V14.S4, V12.S4, V21.S4 // im = -round(B2*wD) + A2*wC

	MDCT_STORE_STAGE

	SUBS $1, R6
	BNE  fold1loop

fold1done:
	RET

// func mdctFold3StoreNeon(dst []kissCpx, bitrev []int, samples []float32, window []float32, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, preScale float32)
//
// Vectorizes the trailing windowed fold. Per output i = i0+j:
//
//	re = round(s[xp2-2j]*w[wp2-2j]) - round(s[xp1-n2+2j]*w[wp1+2j])
//	im = fma(s[xp1+2j], w[wp2-2j], round(s[xp2+n2-2j]*w[wp1+2j]))
//
// followed by the common store stage. Bit-identical per element to the scalar
// mdctMulSubMixAlt/mdctMulAddMix + mdctStoreDirectStageFMALike sequence.
TEXT ·mdctFold3StoreNeon(SB), NOSPLIT, $0-188
	MOVD  dst_base+0(FP), R0
	MOVD  bitrev_base+24(FP), R1
	MOVD  samples_base+48(FP), R2
	MOVD  window_base+72(FP), R3
	MOVD  trig_base+96(FP), R4
	MOVD  i0+120(FP), R10
	MOVD  n4+128(FP), R11
	MOVD  n2+136(FP), R12
	MOVD  xp1+144(FP), R13
	MOVD  xp2+152(FP), R14
	MOVD  wp1+160(FP), R15
	MOVD  wp2+168(FP), R16
	MOVD  blocks+176(FP), R6
	FMOVS preScale+184(FP), F31
	VDUP  V31.S[0], V31.S4

	ADD R10<<3, R1, R1  // &bitrev[i0]
	ADD R10<<2, R4, R4  // &trig[i0]
	ADD R11<<2, R4, R5  // &trig[n4+i0]

	// Ascending streams.
	ADD R13<<2, R2, R17 // &s[xp1]
	SUB R12<<2, R17, R19 // &s[xp1-n2]
	ADD R15<<2, R3, R20 // &w[wp1]
	// Descending stream block bases.
	SUB $6, R14, R21
	ADD R21<<2, R2, R21 // &s[xp2-6]
	ADD R12<<2, R21, R22 // &s[xp2+n2-6]
	SUB $6, R16, R23
	ADD R23<<2, R3, R23 // &w[wp2-6]

	CBZ R6, fold3done

fold3loop:
	VLD2.P 32(R19), [V10.S4, V11.S4] // A3 = s[xp1-n2+2j]
	VLD2.P 32(R17), [V12.S4, V13.S4] // A2 = s[xp1+2j]
	VLD2.P 32(R20), [V14.S4, V15.S4] // wC = w[wp1+2j]
	LOAD_DESC(R21, V16, V17)         // B  = s[xp2-2j]
	LOAD_DESC(R22, V24, V25)         // B4 = s[xp2+n2-2j]
	LOAD_DESC(R23, V26, V27)         // wD = w[wp2-2j]

	WORD $0x6E3ADE13           // FMUL V19.4S, V16.4S, V26.4S (B*wD, rounded)
	WORD $0x6E2EDD56           // FMUL V22.4S, V10.4S, V14.4S (A3*wC, rounded)
	WORD $0x4EB6D674           // FSUB V20.4S, V19.4S, V22.4S (re)
	WORD $0x6E2EDF15           // FMUL V21.4S, V24.4S, V14.4S (B4*wC, rounded)
	VFMLA V26.S4, V12.S4, V21.S4 // im = round(B4*wC) + A2*wD

	MDCT_STORE_STAGE

	SUBS $1, R6
	BNE  fold3loop

fold3done:
	RET

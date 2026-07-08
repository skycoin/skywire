//go:build arm64 && !purego
#include "textflag.h"

// func convertFloat32ToInt16UnitBlocks(dst []int16, src []float32, n int) bool
//
// Converts complete 16-sample blocks from the already-soft-clipped path where
// every sample is in [-1, 1]. Returns false on the first out-of-range or NaN
// sample so the Go soft-clip fallback can process the whole frame.
TEXT ·convertFloat32ToInt16UnitBlocks(SB), NOSPLIT, $0-57
	MOVD  dst_base+0(FP), R0
	MOVD  src_base+24(FP), R1
	MOVD  n+48(FP), R2

	CBZ   R2, convert_done
	FMOVS $1.0, F1
	FMOVS $32768.0, F2
	MOVD  $32767, R5
	MOVD  $-1, R12

	FMOVS $1.0, F3
	WORD  $0x4e040463              // DUP V3.4S, V3.S[0]
	FMOVS $32768.0, F4
	WORD  $0x4e040484              // DUP V4.4S, V4.S[0]
	VDUP  R5, V8.S4

	CMP   $16, R2
	BLT   convert_done

convert_vector16_loop:
	VLD1.P 16(R1), [V0.S4]
	WORD   $0x4ea0f801             // FABS V1.4S, V0.4S
	WORD   $0x6e21e462             // FCMGE V2.4S, V3.4S, V1.4S
	VMOV   V2.D[0], R10
	VMOV   V2.D[1], R11
	CMP    R12, R10
	BNE    convert_fallback
	CMP    R12, R11
	BNE    convert_fallback

	WORD   $0x6e24dc00             // FMUL V0.4S, V0.4S, V4.4S
	WORD   $0x4e21a805             // FCVTNS V5.4S, V0.4S
	WORD   $0x4ea86ca5             // SMIN V5.4S, V5.4S, V8.4S
	WORD   $0x0e6148a9             // SQXTN V9.4H, V5.4S
	VST1.P [V9.H4], 8(R0)

	VLD1.P 16(R1), [V0.S4]
	WORD   $0x4ea0f801             // FABS V1.4S, V0.4S
	WORD   $0x6e21e462             // FCMGE V2.4S, V3.4S, V1.4S
	VMOV   V2.D[0], R10
	VMOV   V2.D[1], R11
	CMP    R12, R10
	BNE    convert_fallback
	CMP    R12, R11
	BNE    convert_fallback

	WORD   $0x6e24dc00             // FMUL V0.4S, V0.4S, V4.4S
	WORD   $0x4e21a805             // FCVTNS V5.4S, V0.4S
	WORD   $0x4ea86ca5             // SMIN V5.4S, V5.4S, V8.4S
	WORD   $0x0e6148a9             // SQXTN V9.4H, V5.4S
	VST1.P [V9.H4], 8(R0)

	VLD1.P 16(R1), [V0.S4]
	WORD   $0x4ea0f801             // FABS V1.4S, V0.4S
	WORD   $0x6e21e462             // FCMGE V2.4S, V3.4S, V1.4S
	VMOV   V2.D[0], R10
	VMOV   V2.D[1], R11
	CMP    R12, R10
	BNE    convert_fallback
	CMP    R12, R11
	BNE    convert_fallback

	WORD   $0x6e24dc00             // FMUL V0.4S, V0.4S, V4.4S
	WORD   $0x4e21a805             // FCVTNS V5.4S, V0.4S
	WORD   $0x4ea86ca5             // SMIN V5.4S, V5.4S, V8.4S
	WORD   $0x0e6148a9             // SQXTN V9.4H, V5.4S
	VST1.P [V9.H4], 8(R0)

	VLD1.P 16(R1), [V0.S4]
	WORD   $0x4ea0f801             // FABS V1.4S, V0.4S
	WORD   $0x6e21e462             // FCMGE V2.4S, V3.4S, V1.4S
	VMOV   V2.D[0], R10
	VMOV   V2.D[1], R11
	CMP    R12, R10
	BNE    convert_fallback
	CMP    R12, R11
	BNE    convert_fallback

	WORD   $0x6e24dc00             // FMUL V0.4S, V0.4S, V4.4S
	WORD   $0x4e21a805             // FCVTNS V5.4S, V0.4S
	WORD   $0x4ea86ca5             // SMIN V5.4S, V5.4S, V8.4S
	WORD   $0x0e6148a9             // SQXTN V9.4H, V5.4S
	VST1.P [V9.H4], 8(R0)

	SUBS  $16, R2
	CMP   $16, R2
	BGE   convert_vector16_loop

convert_done:
	MOVD  $1, R6
	MOVB  R6, ret+56(FP)
	RET

convert_fallback:
	MOVB  ZR, ret+56(FP)
	RET

// func convertFloat32ToInt16SaturatingBlocks(dst []int16, src []float32, n int)
//
// Converts complete 16-sample blocks with round-to-nearest-even (FCVTNS) and
// saturation, matching libopus float2int (lrintf) under the default IEEE
// rounding mode used by celt_float2int16_c.
TEXT ·convertFloat32ToInt16SaturatingBlocks(SB), NOSPLIT, $0-56
	MOVD  dst_base+0(FP), R0
	MOVD  src_base+24(FP), R1
	MOVD  n+48(FP), R2

	CBZ   R2, saturating_done
	FMOVS $32768.0, F4
	WORD  $0x4e040484              // DUP V4.4S, V4.S[0]

	CMP   $16, R2
	BLT   saturating_done

saturating_vector16_loop:
	VLD1.P 16(R1), [V0.S4]
	WORD   $0x6e24dc00             // FMUL V0.4S, V0.4S, V4.4S
	WORD   $0x4e21a805             // FCVTNS V5.4S, V0.4S
	WORD   $0x0e6148a9             // SQXTN V9.4H, V5.4S
	VST1.P [V9.H4], 8(R0)

	VLD1.P 16(R1), [V0.S4]
	WORD   $0x6e24dc00             // FMUL V0.4S, V0.4S, V4.4S
	WORD   $0x4e21a805             // FCVTNS V5.4S, V0.4S
	WORD   $0x0e6148a9             // SQXTN V9.4H, V5.4S
	VST1.P [V9.H4], 8(R0)

	VLD1.P 16(R1), [V0.S4]
	WORD   $0x6e24dc00             // FMUL V0.4S, V0.4S, V4.4S
	WORD   $0x4e21a805             // FCVTNS V5.4S, V0.4S
	WORD   $0x0e6148a9             // SQXTN V9.4H, V5.4S
	VST1.P [V9.H4], 8(R0)

	VLD1.P 16(R1), [V0.S4]
	WORD   $0x6e24dc00             // FMUL V0.4S, V0.4S, V4.4S
	WORD   $0x4e21a805             // FCVTNS V5.4S, V0.4S
	WORD   $0x0e6148a9             // SQXTN V9.4H, V5.4S
	VST1.P [V9.H4], 8(R0)

	SUBS  $16, R2
	CMP   $16, R2
	BGE   saturating_vector16_loop

saturating_done:
	RET

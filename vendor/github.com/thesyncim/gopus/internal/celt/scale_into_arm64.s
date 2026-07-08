//go:build arm64 && !purego

#include "textflag.h"

// FMUL Vd.4S, Vn.4S, Vm.4S = 0x6E20DC00 | (Vm<<16) | (Vn<<5) | Vd

// func scaleFloat32IntoNEON(dst, src []float32, gain float32)
//
// dst[i] = src[i] * gain for i in 0..min(len(dst),len(src))-1. Each lane is a
// bare FMUL (no a*b+c), so it is bit-exact with the scalar reference on every
// build. Callers gate this on a length threshold; the kernel still handles short
// tails for safety.
//
// Register map:
//   R0 = dst, R1 = src; R2 = n; R4 = 8-elem iters; R5 = tail count
//   V0 = gain broadcast
TEXT ·scaleFloat32IntoNEON(SB), NOSPLIT, $0-52
	MOVD dst_base+0(FP), R0
	MOVD dst_len+8(FP), R2
	MOVD src_base+24(FP), R1
	MOVD src_len+32(FP), R3
	FMOVS gain+48(FP), F0

	CMP  R3, R2
	CSEL LT, R2, R3, R2           // R2 = min(len(dst), len(src))
	CBZ  R2, si_done

	VDUP V0.S[0], V0.S4

	LSR  $3, R2, R4               // 8 elements per iteration
	AND  $7, R2, R5
	CBZ  R4, si_tail4

si_loop8:
	VLD1.P 32(R1), [V1.S4, V2.S4]
	WORD $0x6E20DC23            // FMUL V3.4S, V1.4S, V0.4S
	WORD $0x6E20DC44            // FMUL V4.4S, V2.4S, V0.4S
	VST1.P [V3.S4, V4.S4], 32(R0)
	SUBS $1, R4
	BNE  si_loop8

si_tail4:
	TBZ $2, R5, si_tail2
	VLD1.P 16(R1), [V1.S4]
	WORD $0x6E20DC23            // FMUL V3.4S, V1.4S, V0.4S
	VST1.P [V3.S4], 16(R0)

si_tail2:
	TBZ $1, R5, si_tail1
	FMOVD (R1), F1               // two floats into V1 low
	VDUP  V1.D[0], V1.D2
	WORD $0x6E20DC23            // FMUL V3.4S, V1.4S, V0.4S (lanes 0,1 used)
	FMOVD F3, (R0)
	ADD   $8, R0
	ADD   $8, R1

si_tail1:
	TBZ $0, R5, si_done
	FMOVS (R1), F1
	FMULS F0, F1, F1
	FMOVS F1, (R0)

si_done:
	RET

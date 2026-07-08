//go:build arm64 && !purego

#include "textflag.h"

// func floatToInt16ScaledCore(out []int16, in []float32, scale float32, n int)
//
// Writes out[i] = sat16(round_even(in[i]*scale)) for i in [0,n), with n a
// multiple of 8 (the Go wrapper handles the remainder). FCVTNS rounds to
// nearest with ties to even and SQXTN saturates to int16, which is bit-exact
// with the scalar saturate-then-round-even path, so every target stays
// byte-identical.
TEXT ·floatToInt16ScaledCore(SB), NOSPLIT, $0-64
	MOVD  out_base+0(FP), R0
	MOVD  in_base+24(FP), R1
	FMOVS scale+48(FP), F3
	MOVD  n+56(FP), R2

	CBZ  R2, done
	WORD $0x4e040463              // DUP V3.4S, V3.S[0]

loop8:
	VLD1.P 32(R1), [V1.S4, V2.S4]
	WORD   $0x6e23dc21            // FMUL V1.4S, V1.4S, V3.4S
	WORD   $0x6e23dc42            // FMUL V2.4S, V2.4S, V3.4S
	WORD   $0x4e21a821            // FCVTNS V1.4S, V1.4S
	WORD   $0x4e21a842            // FCVTNS V2.4S, V2.4S
	WORD   $0x0e614820            // SQXTN V0.4H, V1.4S
	WORD   $0x4e614840            // SQXTN2 V0.8H, V2.4S
	VST1.P [V0.H8], 16(R0)

	SUBS $8, R2
	BNE  loop8

done:
	RET

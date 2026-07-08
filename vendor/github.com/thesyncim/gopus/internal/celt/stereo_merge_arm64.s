//go:build arm64 && !purego

#include "textflag.h"

// Vector float ops the Go arm64 assembler lacks mnemonics for. Encodings:
//   FMUL Vd.4S, Vn.4S, Vm.4S = 0x6E20DC00 | (Vm<<16) | (Vn<<5) | Vd
//   FADD Vd.4S, Vn.4S, Vm.4S = 0x4E20D400 | (Vm<<16) | (Vn<<5) | Vd
//   FSUB Vd.4S, Vn.4S, Vm.4S = 0x4EA0D400 | (Vm<<16) | (Vn<<5) | Vd

// func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32)
//
// In-place final rescale of stereoMerge over n=len(x) lanes:
//   l    = mid * x[i]        (rounded, no a*b+c contraction)
//   x[i] = lgain * (l - y[i])
//   y[i] = rgain * (l + y[i])
// Every op is a bare FMUL/FADD/FSUB (the scalar path uses noFMA32*), so this is
// bit-exact with the scalar reference on every build, fused or not.
//
// Register map:
//   R0 = x base, R1 = y base; R2 = n; R3 = vec iters (n/4); R4 = scalar tail
//   V0 = mid, V1 = lgain, V2 = rgain (broadcast)
//   V3 = x, V4 = y; V5 = l; V6 = l-y, V7 = l+y; V8 = x out, V9 = y out
TEXT ·stereoMergeRescaleNEON(SB), NOSPLIT, $0-60
	MOVD x_base+0(FP), R0
	MOVD x_len+8(FP), R2
	MOVD y_base+24(FP), R1
	FMOVS mid+48(FP), F0
	FMOVS lgain+52(FP), F1
	FMOVS rgain+56(FP), F2

	CBZ R2, sm_done

	VDUP V0.S[0], V0.S4
	VDUP V1.S[0], V1.S4
	VDUP V2.S[0], V2.S4

	LSR  $2, R2, R3
	AND  $3, R2, R4
	CBZ  R3, sm_tail

sm_loop4:
	VLD1 (R0), [V3.S4]            // x
	VLD1 (R1), [V4.S4]            // y

	WORD $0x6E20DC65            // FMUL V5.4S, V3.4S, V0.4S  (l = mid*x)
	WORD $0x4EA4D4A6           // FSUB V6.4S, V5.4S, V4.4S  (l - y)
	WORD $0x4E24D4A7          // FADD V7.4S, V5.4S, V4.4S  (l + y)
	WORD $0x6E21DCC8         // FMUL V8.4S, V6.4S, V1.4S  (x = lgain*(l-y))
	WORD $0x6E22DCE9        // FMUL V9.4S, V7.4S, V2.4S  (y = rgain*(l+y))

	VST1 [V8.S4], (R0)
	VST1 [V9.S4], (R1)
	ADD  $16, R0
	ADD  $16, R1

	SUBS $1, R3
	BNE  sm_loop4

sm_tail:
	CBZ  R4, sm_done

sm_tail1:
	FMOVS (R0), F3
	FMOVS (R1), F4
	FMULS F0, F3, F5             // l = mid*x
	FSUBS F4, F5, F6            // l - y
	FADDS F4, F5, F7           // l + y
	FMULS F1, F6, F8          // lgain*(l-y)
	FMULS F2, F7, F9         // rgain*(l+y)
	FMOVS F8, (R0)
	FMOVS F9, (R1)
	ADD   $4, R0
	ADD   $4, R1
	SUBS  $1, R4
	BNE   sm_tail1

sm_done:
	RET

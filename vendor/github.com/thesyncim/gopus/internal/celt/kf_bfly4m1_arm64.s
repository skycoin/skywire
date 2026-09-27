//go:build (arm64) && !purego

#include "textflag.h"

// func kfBfly4M1Core(fout []kissCpx, n int)
//
// Twiddle-free radix-4 butterflies on contiguous groups of four complex
// values. The main loop runs two butterflies per NEON iteration: VLD4 with a
// D arrangement deinterleaves the four legs across both butterflies, the
// add/sub network operates on r/i lanes together, and the +-i swizzle is
// REV64 followed by an exact multiply with {1,-1,1,-1}. Lane order matches
// the scalar sequence exactly, so results are bit-identical; a scalar tail
// handles odd n.
TEXT ·kfBfly4M1Core(SB), NOSPLIT, $0-32
	MOVD fout_base+0(FP), R0
	MOVD n+24(FP), R1
	CMP  $0, R1
	BLE  bfly4m1_done

	// V16 = {1, -1, 1, -1}
	FMOVS $-1.0, F16
	VDUP  V16.S[0], V16.S4
	FMOVS $1.0, F17
	VMOV  V17.S[0], V16.S[0]
	VMOV  V17.S[0], V16.S[2]

	LSR $1, R1, R2 // two-butterfly blocks
	AND $1, R1, R1 // scalar tail
	CBZ R2, bfly4m1_tail

bfly4m1_vec:
	VLD4 (R0), [V0.D2, V1.D2, V2.D2, V3.D2]

	WORD $0x4EA2D404 // FSUB V4, V0, V2 (s0)
	WORD $0x4E22D405 // FADD V5, V0, V2 (f0)
	WORD $0x4E23D426 // FADD V6, V1, V3 (s1 sum)
	WORD $0x4EA6D4A7 // FSUB V7, V5, V6 (f2)
	WORD $0x4E26D4A5 // FADD V5, V5, V6 (f0 += s1)
	WORD $0x4EA3D426 // FSUB V6, V1, V3 (s1 diff)
	VREV64 V6.S4, V6.S4
	WORD $0x6E30DCC6 // FMUL V6, V6, V16 ({s1i, -s1r} per pair, exact)
	WORD $0x4E26D489 // FADD V9, V4, V6 (f1)
	WORD $0x4EA6D48A // FSUB V10, V4, V6 (f3)

	VZIP1 V9.D2, V5.D2, V11.D2  // b0: f0, f1
	VZIP1 V10.D2, V7.D2, V12.D2 // b0: f2, f3
	VZIP2 V9.D2, V5.D2, V13.D2  // b1: f0, f1
	VZIP2 V10.D2, V7.D2, V14.D2 // b1: f2, f3
	VST1.P [V11.S4, V12.S4, V13.S4, V14.S4], 64(R0)

	SUBS $1, R2
	BNE  bfly4m1_vec

bfly4m1_tail:
	CBZ  R1, bfly4m1_done

	FMOVS (R0), F0
	FMOVS 4(R0), F1
	FMOVS 8(R0), F2
	FMOVS 12(R0), F3
	FMOVS 16(R0), F4
	FMOVS 20(R0), F5
	FMOVS 24(R0), F6
	FMOVS 28(R0), F7

	FSUBS F4, F0, F8  // s0r
	FSUBS F5, F1, F9  // s0i
	FADDS F4, F0, F0  // f0r
	FADDS F5, F1, F1  // f0i

	FADDS F6, F2, F10 // s1r sum
	FADDS F7, F3, F11 // s1i sum
	FSUBS F10, F0, F12 // f2r
	FSUBS F11, F1, F13 // f2i
	FADDS F10, F0, F0  // f0r += s1r
	FADDS F11, F1, F1

	FSUBS F6, F2, F10 // s1r diff
	FSUBS F7, F3, F11 // s1i diff
	FADDS F11, F8, F14 // f1r = s0r + s1i
	FSUBS F10, F9, F15 // f1i = s0i - s1r
	FSUBS F11, F8, F6  // f3r = s0r - s1i
	FADDS F10, F9, F7  // f3i = s0i + s1r

	FMOVS F0, (R0)
	FMOVS F1, 4(R0)
	FMOVS F14, 8(R0)
	FMOVS F15, 12(R0)
	FMOVS F12, 16(R0)
	FMOVS F13, 20(R0)
	FMOVS F6, 24(R0)
	FMOVS F7, 28(R0)

bfly4m1_done:
	RET

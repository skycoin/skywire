//go:build (arm64) && !purego

#include "textflag.h"

// func kfBfly4M1Core(fout []kissCpx, n int)
TEXT ·kfBfly4M1Core(SB), NOSPLIT, $0-32
	MOVD fout_base+0(FP), R0
	MOVD n+24(FP), R1
	CMP  $0, R1
	BLE  bfly4m1_done

bfly4m1_loop:
	FMOVS (R0), F0
	FMOVS 4(R0), F1
	FMOVS 8(R0), F2
	FMOVS 12(R0), F3
	FMOVS 16(R0), F4
	FMOVS 20(R0), F5
	FMOVS 24(R0), F6
	FMOVS 28(R0), F7

	// s0 = a0 - a2; f0 = a0 + a2
	FSUBS F4, F0, F8
	FSUBS F5, F1, F9
	FADDS F4, F0, F10
	FADDS F5, F1, F11

	// s1 = a1 + a3; f2 = f0 - s1; f0 += s1
	FADDS F6, F2, F12
	FADDS F7, F3, F13
	FSUBS F12, F10, F14
	FSUBS F13, F11, F15
	FADDS F12, F10, F10
	FADDS F13, F11, F11

	// s1 = a1 - a3
	FSUBS F6, F2, F16
	FSUBS F7, F3, F17

	// f1 = (s0.r+s1.i, s0.i-s1.r); f3 = (s0.r-s1.i, s0.i+s1.r)
	FADDS F17, F8, F18
	FSUBS F16, F9, F19
	FSUBS F17, F8, F20
	FADDS F16, F9, F21

	FMOVS F10, (R0)
	FMOVS F11, 4(R0)
	FMOVS F18, 8(R0)
	FMOVS F19, 12(R0)
	FMOVS F14, 16(R0)
	FMOVS F15, 20(R0)
	FMOVS F20, 24(R0)
	FMOVS F21, 28(R0)

	ADD  $32, R0, R0
	SUBS $1, R1, R1
	BNE  bfly4m1_loop

bfly4m1_done:
	RET

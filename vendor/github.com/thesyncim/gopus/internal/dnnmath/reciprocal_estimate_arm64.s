//go:build arm64 && !purego
#include "textflag.h"

TEXT ·reciprocalEstimate32(SB), NOSPLIT, $0-12
	FMOVS x+0(FP), F0
	WORD  $0x5ea1d800 // frecpe s0, s0
	FMOVS F0, ret+8(FP)
	RET

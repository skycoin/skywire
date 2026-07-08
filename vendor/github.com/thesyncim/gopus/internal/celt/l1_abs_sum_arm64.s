//go:build arm64 && !purego

#include "textflag.h"

// func l1AbsSumNeon(tmp []float32, n int) float32
//
// Returns sum_{i=0}^{n-1} |tmp[i]| using four NEON lane accumulators plus a
// scalar tail. The 4-lane reduction order differs from the strict left-to-right
// scalar sum by a few ULP; this is the arm64 quality-gated regime (MODEL A).
// amd64 and purego keep the scalar L1 accumulation for the byte-exact gate.
TEXT ·l1AbsSumNeon(SB), NOSPLIT, $0-36
	MOVD tmp_base+0(FP), R0
	MOVD n+24(FP), R1

	VEOR V0.B16, V0.B16, V0.B16
	FMOVS ZR, F2

	CMP $0, R1
	BLE done

loop4:
	CMP $4, R1
	BLT tail

	VLD1.P 16(R0), [V1.S4]
	WORD $0x4ea0f821            // FABS V1.4S, V1.4S
	WORD $0x4e21d400            // FADD V0.4S, V0.4S, V1.4S

	SUB $4, R1
	B loop4

tail:
	CBZ R1, reduce
	FMOVS.P 4(R0), F1
	FABSS F1, F1
	FADDS F1, F2, F2
	SUB $1, R1
	B tail

reduce:
	WORD $0x6e20d400            // FADDP V0.4S, V0.4S, V0.4S
	WORD $0x7e30d800            // FADDP S0, V0.2S
	FADDS F2, F0, F0

done:
	FMOVS F0, ret+32(FP)
	RET

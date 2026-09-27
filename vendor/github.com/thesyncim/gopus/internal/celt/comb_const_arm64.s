//go:build arm64 && !purego

#include "textflag.h"

// func combFilterConstNeon(dst, delay []float32, g10, g11, g12 float32, blocks int)
//
// Runs blocks*4 steps of the constant-gain comb filter body. Per output i
// (with delay positioned so delay[i] pairs dst[i], and delay[i-4..i-1]
// available below the slice start passed by the caller):
//
//	dst[i] += g10*delay[i-2] + g11*(delay[i-1]+delay[i-3]) + g12*(delay[i]+delay[i-4])
//
// computed exactly like the scalar combFilterConstValue with arm64
// contraction: the two tap sums round as plain adds, then three fused FMLAs
// accumulate in g10, g11, g12 order — bit-identical per lane. The caller
// guarantees the comb period is at least combFilterMinPeriod, so a 4-wide
// block never reads a lane another lane wrote.
TEXT ·combFilterConstNeon(SB), NOSPLIT, $0-72
	MOVD  dst_base+0(FP), R0
	MOVD  delay_base+24(FP), R1
	FMOVS g10+48(FP), F16
	FMOVS g11+52(FP), F17
	FMOVS g12+56(FP), F18
	MOVD  blocks+64(FP), R2

	CBZ  R2, comb_done

	VDUP V16.S[0], V16.S4
	VDUP V17.S[0], V17.S4
	VDUP V18.S[0], V18.S4

	// R1 starts at &delay[i-4]; keep the trailing window in V0.
	VLD1.P 16(R1), [V0.S4]

comb_loop:
	VLD1 (R1), [V1.S4] // delay[i..i+3]
	ADD  $16, R1

	VEXT $4, V1.B16, V0.B16, V2.B16  // delay[i-3..i]   (minus1)
	VEXT $8, V1.B16, V0.B16, V3.B16  // delay[i-2..i+1] (center)
	VEXT $12, V1.B16, V0.B16, V4.B16 // delay[i-1..i+2] (plus1)

	WORD $0x4E22D485 // FADD V5.4S, V4.4S, V2.4S (plus1+minus1)
	WORD $0x4E20D426 // FADD V6.4S, V1.4S, V0.4S (plus2+minus2)

	VLD1 (R0), [V7.S4]
	VFMLA V16.S4, V3.S4, V7.S4 // += g10*center
	VFMLA V17.S4, V5.S4, V7.S4 // += g11*(plus1+minus1)
	VFMLA V18.S4, V6.S4, V7.S4 // += g12*(plus2+minus2)
	VST1.P [V7.S4], 16(R0)

	VMOV V1.B16, V0.B16

	SUBS $1, R2
	BNE  comb_loop

comb_done:
	RET

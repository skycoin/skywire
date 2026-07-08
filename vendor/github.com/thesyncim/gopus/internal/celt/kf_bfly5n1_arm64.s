//go:build arm64 && !purego
#include "textflag.h"

// func kfBfly5N1(fout []kissCpx, tw []kissCpx, m, fstride int)
//
// Common CELT radix-5 stage specialization for N=1, mm=1.
// This keeps rolling pointers for both fout lanes and twiddles, avoiding the
// outer-loop/index rebuild that the general kfBfly5Inner path pays every call.
TEXT ·kfBfly5N1(SB), NOSPLIT, $0-64
	MOVD fout_base+0(FP), R0
	MOVD tw_base+24(FP), R1
	MOVD m+48(FP), R2
	MOVD fstride+56(FP), R3

	CMP  $1, R2
	BLT  done
	CMP  $1, R3
	BLT  done

	// Load ya = tw[fstride*m], yb = tw[2*fstride*m].
	MUL  R3, R2, R4
	LSL  $3, R4, R5
	ADD  R1, R5, R5
	FMOVS (R5), F24
	FMOVS 4(R5), F25

	LSL  $1, R4, R4
	LSL  $3, R4, R5
	ADD  R1, R5, R5
	FMOVS (R5), F26
	FMOVS 4(R5), F27

	// fout pointers: idx0..idx4
	LSL  $3, R2, R12
	MOVD R0, R4
	ADD  R12, R4, R5
	ADD  R12, R5, R6
	ADD  R12, R6, R7
	ADD  R12, R7, R8

	// twiddle pointers w1..w4
	MOVD R1, R9
	MOVD R1, R10
	MOVD R1, R11
	MOVD R1, R12

	// twiddle pointer increments
	LSL  $3, R3, R13
	LSL  $1, R13, R14
	ADD  R13, R14, R15
	LSL  $2, R13, R16

	MOVD R2, R17

loop:
	// s0 = fout[idx0]
	FMOVS (R4), F0
	FMOVS 4(R4), F1

	// s1 = cmul(fout[idx1], w1)
	FMOVS (R5), F2
	FMOVS 4(R5), F3
	FMOVS (R9), F28
	FMOVS 4(R9), F29
	FMULS F3, F29, F30
	FNEGS F30, F30
	FMADDS F28, F30, F2, F30
	FMULS F3, F28, F31
	FMADDS F29, F31, F2, F31
	FMOVS F30, F2
	FMOVS F31, F3

	// s2 = cmul(fout[idx2], w2)
	FMOVS (R6), F4
	FMOVS 4(R6), F5
	FMOVS (R10), F28
	FMOVS 4(R10), F29
	FMULS F5, F29, F30
	FNEGS F30, F30
	FMADDS F28, F30, F4, F30
	FMULS F5, F28, F31
	FMADDS F29, F31, F4, F31
	FMOVS F30, F4
	FMOVS F31, F5

	// s3 = cmul(fout[idx3], w3)
	FMOVS (R7), F6
	FMOVS 4(R7), F7
	FMOVS (R11), F28
	FMOVS 4(R11), F29
	FMULS F7, F29, F30
	FNEGS F30, F30
	FMADDS F28, F30, F6, F30
	FMULS F7, F28, F31
	FMADDS F29, F31, F6, F31
	FMOVS F30, F6
	FMOVS F31, F7

	// s4 = cmul(fout[idx4], w4)
	FMOVS (R8), F16
	FMOVS 4(R8), F17
	FMOVS (R12), F28
	FMOVS 4(R12), F29
	FMULS F17, F29, F30
	FNEGS F30, F30
	FMADDS F28, F30, F16, F30
	FMULS F17, F28, F31
	FMADDS F29, F31, F16, F31
	FMOVS F30, F16
	FMOVS F31, F17

	// s10 = s1 - s4, s7 = s1 + s4
	FSUBS F16, F2, F18
	FSUBS F17, F3, F19
	FADDS F16, F2, F2
	FADDS F17, F3, F3

	// s9 = s2 - s3, s8 = s2 + s3
	FSUBS F6, F4, F20
	FSUBS F7, F5, F21
	FADDS F6, F4, F4
	FADDS F7, F5, F5

	// fout[idx0] = s0 + s7 + s8
	FADDS F4, F2, F6
	FADDS F6, F0, F6
	FADDS F5, F3, F7
	FADDS F7, F1, F7
	FMOVS F6, (R4)
	FMOVS F7, 4(R4)

	// s5 = s0 + (s7*ya + s8*yb)
	FMULS F4, F26, F16
	FMADDS F24, F16, F2, F16
	FADDS F16, F0, F16
	FMULS F5, F26, F17
	FMADDS F24, F17, F3, F17
	FADDS F17, F1, F17

	// s6 = [s10.i*yai + s9.i*ybi, -(s10.r*yai + s9.r*ybi)]
	FMULS F21, F27, F22
	FMADDS F25, F22, F19, F22
	FMULS F20, F27, F23
	FMADDS F25, F23, F18, F23
	FNEGS F23, F23

	// fout[idx1] = s5 - s6
	FSUBS F22, F16, F6
	FSUBS F23, F17, F7
	FMOVS F6, (R5)
	FMOVS F7, 4(R5)

	// fout[idx4] = s5 + s6
	FADDS F22, F16, F6
	FADDS F23, F17, F7
	FMOVS F6, (R8)
	FMOVS F7, 4(R8)

	// s11 = s0 + (s7*yb + s8*ya)
	FMULS F4, F24, F16
	FMADDS F26, F16, F2, F16
	FADDS F16, F0, F16
	FMULS F5, F24, F17
	FMADDS F26, F17, F3, F17
	FADDS F17, F1, F17

	// s12 = [s9.i*yai - s10.i*ybi, s10.r*ybi - s9.r*yai]
	FMULS F19, F27, F22
	FNEGS F22, F22
	FMADDS F25, F22, F21, F22
	FMULS F20, F25, F23
	FNEGS F23, F23
	FMADDS F27, F23, F18, F23

	// fout[idx2] = s11 + s12
	FADDS F22, F16, F6
	FADDS F23, F17, F7
	FMOVS F6, (R6)
	FMOVS F7, 4(R6)

	// fout[idx3] = s11 - s12
	FSUBS F22, F16, F6
	FSUBS F23, F17, F7
	FMOVS F6, (R7)
	FMOVS F7, 4(R7)

	ADD  $8, R4, R4
	ADD  $8, R5, R5
	ADD  $8, R6, R6
	ADD  $8, R7, R7
	ADD  $8, R8, R8
	ADD  R13, R9, R9
	ADD  R14, R10, R10
	ADD  R15, R11, R11
	ADD  R16, R12, R12

	SUBS $1, R17, R17
	BNE  loop

done:
	RET

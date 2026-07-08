//go:build arm64 && !purego
#include "textflag.h"

// func writeInt16AsFloat32Core(dst []float32, src []int16, n int)
TEXT ·writeInt16AsFloat32Core(SB), NOSPLIT, $0-56
	MOVD dst_base+0(FP), R0
	MOVD src_base+24(FP), R1
	MOVD n+48(FP), R2

	CBZ   R2, done
	FMOVS $0.000030517578125, F3
	WORD  $0x4e040463              // DUP V3.4S, V3.S[0]

	CMP $8, R2
	BLT tail

loop8:
	VLD1.P 16(R1), [V0.H8]
	WORD   $0x0f10a401             // SSHLL V1.4S, V0.4H, #0
	WORD   $0x4f10a402             // SSHLL2 V2.4S, V0.8H, #0
	WORD   $0x4e21d821             // SCVTF V1.4S, V1.4S
	WORD   $0x4e21d842             // SCVTF V2.4S, V2.4S
	WORD   $0x6e23dc21             // FMUL V1.4S, V1.4S, V3.4S
	WORD   $0x6e23dc42             // FMUL V2.4S, V2.4S, V3.4S
	VST1.P [V1.S4, V2.S4], 32(R0)

	SUBS $8, R2
	CMP  $8, R2
	BGE  loop8

tail:
	CBZ R2, done

tail_loop:
	MOVH    (R1), R4
	SCVTFWS R4, F0
	FMULS   F3, F0, F0
	FMOVS   F0, (R0)
	ADD     $2, R1
	ADD     $4, R0
	SUBS    $1, R2
	BNE     tail_loop

done:
	RET

//go:build amd64 && !purego

#include "textflag.h"

// func celtInnerProdSSEStyleAsm(x, y []celtNorm) float32
//
// Mirrors libopus 1.6.1 celt/x86/pitch_sse.c:celt_inner_prod_sse:
// four-lane MULPS/ADDPS accumulation, horizontal (lane0+lane2)+(lane1+lane3),
// then scalar MAC16_16 tail.
TEXT ·celtInnerProdSSEStyleAsm(SB), NOSPLIT, $16-52
	MOVQ x_base+0(FP), AX
	MOVQ x_len+8(FP), CX
	MOVQ y_base+24(FP), BX

	XORPS X0, X0
	XORQ  DX, DX

	CMPQ CX, $4
	JL   celt_innerprod_hsum

	MOVQ CX, SI
	SUBQ $3, SI

celt_innerprod_loop4:
	MOVUPS (AX)(DX*4), X1
	MOVUPS (BX)(DX*4), X2
	MULPS  X2, X1
	ADDPS  X1, X0
	ADDQ   $4, DX
	CMPQ   DX, SI
	JL     celt_innerprod_loop4

celt_innerprod_hsum:
	MOVUPS X0, 0(SP)
	MOVSS  0(SP), X0
	MOVSS  8(SP), X1
	ADDSS  X1, X0
	MOVSS  4(SP), X1
	MOVSS 12(SP), X2
	ADDSS  X2, X1
	ADDSS  X1, X0

celt_innerprod_tail:
	CMPQ DX, CX
	JGE  celt_innerprod_done
	MOVSS (AX)(DX*4), X1
	MOVSS (BX)(DX*4), X2
	MULSS X2, X1
	ADDSS X1, X0
	INCQ  DX
	JMP   celt_innerprod_tail

celt_innerprod_done:
	MOVSS X0, ret+48(FP)
	RET

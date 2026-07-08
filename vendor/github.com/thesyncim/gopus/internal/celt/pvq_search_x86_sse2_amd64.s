//go:build amd64 && !purego

#include "textflag.h"

// func x86RcpApprox4(dst, src *[4]float32)
TEXT ·x86RcpApprox4(SB), NOSPLIT, $0-16
	MOVQ  dst+0(FP), AX
	MOVQ  src+8(FP), BX
	MOVUPS (BX), X0
	RCPPS X0, X0
	MOVUPS X0, (AX)
	RET

// func x86PVQSearchBestIDSSE2(absX, y []float32, xy, yy float32, n int) int
TEXT ·x86PVQSearchBestIDSSE2(SB), NOSPLIT, $32-72
	MOVQ  absX_base+0(FP), AX
	MOVQ  y_base+24(FP), BX
	MOVSS xy+48(FP), X4
	MOVSS yy+52(FP), X5
	MOVQ  n+56(FP), CX

	TESTQ CX, CX
	JLE   x86_pvq_best_zero

	SHUFPS $0x00, X4, X4
	SHUFPS $0x00, X5, X5
	XORPS  X6, X6
	PXOR   X7, X7
	MOVQ   $0x0000000100000000, R12
	MOVQ   R12, 0(SP)
	MOVQ   $0x0000000300000002, R12
	MOVQ   R12, 8(SP)
	MOVUPS 0(SP), X8
	MOVQ   $0x0000000400000004, R12
	MOVQ   R12, 0(SP)
	MOVQ   R12, 8(SP)
	MOVUPS 0(SP), X9
	XORQ   DX, DX

	MOVQ CX, DI
	ADDQ $3, DI
	ANDQ $~3, DI

x86_pvq_best_loop:
	MOVUPS (AX)(DX*4), X0
	MOVUPS (BX)(DX*4), X1
	ADDPS  X4, X0
	ADDPS  X5, X1
	RSQRTPS X1, X1
	MULPS  X1, X0

	MOVAPS X0, X2
	CMPPS  X6, X2, $6
	PAND   X8, X2
	PMAXSW X2, X7
	MAXPS X0, X6
	PADDD X9, X8

	ADDQ $4, DX
	CMPQ DX, DI
	JL   x86_pvq_best_loop

	MOVAPS X6, X0
	MOVAPS X0, X1
	SHUFPS $0x4e, X1, X1
	MAXPS  X1, X0
	MOVAPS X0, X1
	SHUFPS $0xb1, X1, X1
	MAXPS  X1, X0

	MOVAPS X6, X1
	CMPPS  X0, X1, $0
	PAND   X1, X7
	MOVAPS X7, X1
	PUNPCKHQDQ X1, X1
	PMAXSW X1, X7
	PSHUFLW $0x4e, X7, X1
	PMAXSW X1, X7
	MOVUPS X7, 0(SP)
	MOVL   0(SP), R12

	CMPQ R12, CX
	JLT  x86_pvq_best_return
	XORQ R12, R12
x86_pvq_best_return:
	MOVQ R12, ret+64(FP)
	RET

x86_pvq_best_zero:
	MOVQ $0, ret+64(FP)
	RET

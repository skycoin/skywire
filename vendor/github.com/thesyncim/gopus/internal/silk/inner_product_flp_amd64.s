//go:build amd64 && !purego

#include "textflag.h"

// func innerProductFLPAVX2(a, b []float32, length int) float64
//
// Mirrors libopus silk/float/x86/inner_product_FLP_avx2.c:
// two 256-bit double accumulators, float32-to-float64 conversion before
// multiply, FMA accumulation for 8-sample chunks, one extra 4-sample AVX block,
// then scalar double tail.
TEXT ·innerProductFLPAVX2(SB), NOSPLIT, $0-64
	MOVQ a_base+0(FP), AX
	MOVQ b_base+24(FP), BX
	MOVQ length+48(FP), CX

	VXORPD Y0, Y0, Y0 // accum1
	VXORPD Y1, Y1, Y1 // accum2
	XORQ   DX, DX     // i

	MOVQ CX, SI
	SUBQ $7, SI

loop8:
	CMPQ DX, SI
	JGE  loop4_prep

	VMOVUPS   (AX)(DX*4), X2
	VMOVUPS   (BX)(DX*4), X3
	VCVTPS2PD X2, Y2
	VCVTPS2PD X3, Y3
	VFMADD231PD Y3, Y2, Y0

	VMOVUPS   16(AX)(DX*4), X2
	VMOVUPS   16(BX)(DX*4), X3
	VCVTPS2PD X2, Y2
	VCVTPS2PD X3, Y3
	VFMADD231PD Y3, Y2, Y1

	ADDQ $8, DX
	JMP  loop8

loop4_prep:
	MOVQ CX, SI
	SUBQ $3, SI

loop4:
	CMPQ DX, SI
	JGE  reduce

	VMOVUPS   (AX)(DX*4), X2
	VMOVUPS   (BX)(DX*4), X3
	VCVTPS2PD X2, Y2
	VCVTPS2PD X3, Y3
	VFMADD231PD Y3, Y2, Y0

	ADDQ $4, DX
	JMP  loop4

reduce:
	VADDPD     Y1, Y0, Y0
	VPERM2F128 $0x01, Y0, Y0, Y1
	VADDPD     Y1, Y0, Y0
	VHADDPD    Y0, Y0, Y0

tail:
	CMPQ DX, CX
	JGE  done

	VXORPS    X2, X2, X2
	VXORPS    X3, X3, X3
	VMOVSS    (AX)(DX*4), X2
	VMOVSS    (BX)(DX*4), X3
	VCVTSS2SD X2, X2, X2
	VCVTSS2SD X3, X3, X3
	VMULSD    X3, X2, X2
	VADDSD    X2, X0, X0

	INCQ DX
	JMP  tail

done:
	VMOVSD X0, ret+56(FP)
	VZEROUPPER
	RET

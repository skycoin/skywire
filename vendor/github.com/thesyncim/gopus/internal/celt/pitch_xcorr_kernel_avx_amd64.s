//go:build amd64 && !purego

#include "textflag.h"

// xcorrKernelAVX8Mask holds 7 set lanes followed by 8 clear lanes. Loading 8
// int32 at byte offset (7-r)*4 yields a VMASKMOVPS mask with the low r lanes
// set, matching libopus celt/x86/pitch_avx.c's static mask[15].
GLOBL xcorrKernelAVX8Mask<>(SB), RODATA|NOPTR, $60
DATA xcorrKernelAVX8Mask<>+0(SB)/4, $0xFFFFFFFF
DATA xcorrKernelAVX8Mask<>+4(SB)/4, $0xFFFFFFFF
DATA xcorrKernelAVX8Mask<>+8(SB)/4, $0xFFFFFFFF
DATA xcorrKernelAVX8Mask<>+12(SB)/4, $0xFFFFFFFF
DATA xcorrKernelAVX8Mask<>+16(SB)/4, $0xFFFFFFFF
DATA xcorrKernelAVX8Mask<>+20(SB)/4, $0xFFFFFFFF
DATA xcorrKernelAVX8Mask<>+24(SB)/4, $0xFFFFFFFF
DATA xcorrKernelAVX8Mask<>+28(SB)/4, $0x00000000
DATA xcorrKernelAVX8Mask<>+32(SB)/4, $0x00000000
DATA xcorrKernelAVX8Mask<>+36(SB)/4, $0x00000000
DATA xcorrKernelAVX8Mask<>+40(SB)/4, $0x00000000
DATA xcorrKernelAVX8Mask<>+44(SB)/4, $0x00000000
DATA xcorrKernelAVX8Mask<>+48(SB)/4, $0x00000000
DATA xcorrKernelAVX8Mask<>+52(SB)/4, $0x00000000
DATA xcorrKernelAVX8Mask<>+56(SB)/4, $0x00000000

// func xcorrKernelAVX8(x, y *float32, sum *[8]float32, length int)
//
// Computes eight float cross-correlations sum[c] = sum_j x[j]*y[j+c] for
// c in 0..7, accumulating with fused multiply-add and reducing in the exact
// instruction order of libopus celt/x86/pitch_avx.c:xcorr_kernel_avx so the
// result is bit-identical to that AVX2 reference (and to the scalar lane-order
// fallback it mirrors).
TEXT ·xcorrKernelAVX8(SB), NOSPLIT, $0-32
	MOVQ x+0(FP), AX
	MOVQ y+8(FP), BX
	MOVQ sum+16(FP), DX
	MOVQ length+24(FP), CX

	VXORPS Y0, Y0, Y0
	VXORPS Y1, Y1, Y1
	VXORPS Y2, Y2, Y2
	VXORPS Y3, Y3, Y3
	VXORPS Y4, Y4, Y4
	VXORPS Y5, Y5, Y5
	VXORPS Y6, Y6, Y6
	VXORPS Y7, Y7, Y7

	XORQ SI, SI

	MOVQ CX, DI
	SUBQ $7, DI // DI = length-7 (loop bound, may be <= 0)

main_loop:
	CMPQ SI, DI
	JGE  tail

	// x0 = x[i..i+7]
	VMOVUPS (AX)(SI*4), Y8
	// y base for this iteration
	LEAQ (BX)(SI*4), R8

	VFMADD231PS 0(R8), Y8, Y0
	VFMADD231PS 4(R8), Y8, Y1
	VFMADD231PS 8(R8), Y8, Y2
	VFMADD231PS 12(R8), Y8, Y3
	VFMADD231PS 16(R8), Y8, Y4
	VFMADD231PS 20(R8), Y8, Y5
	VFMADD231PS 24(R8), Y8, Y6
	VFMADD231PS 28(R8), Y8, Y7

	ADDQ $8, SI
	JMP  main_loop

tail:
	// remaining r = length - i; if zero, skip.
	MOVQ CX, R9
	SUBQ SI, R9
	JLE  reduce

	// mask = load 8 int32 at xcorrKernelAVX8Mask + (7-r)*4
	MOVQ $7, R10
	SUBQ R9, R10 // R10 = 7 - r
	LEAQ xcorrKernelAVX8Mask<>(SB), R11
	VMOVUPS (R11)(R10*4), Y9 // Y9 = lane mask

	VMASKMOVPS (AX)(SI*4), Y9, Y8 // x0 masked
	LEAQ       (BX)(SI*4), R8

	VMASKMOVPS 0(R8), Y9, Y10
	VFMADD231PS Y10, Y8, Y0
	VMASKMOVPS 4(R8), Y9, Y10
	VFMADD231PS Y10, Y8, Y1
	VMASKMOVPS 8(R8), Y9, Y10
	VFMADD231PS Y10, Y8, Y2
	VMASKMOVPS 12(R8), Y9, Y10
	VFMADD231PS Y10, Y8, Y3
	VMASKMOVPS 16(R8), Y9, Y10
	VFMADD231PS Y10, Y8, Y4
	VMASKMOVPS 20(R8), Y9, Y10
	VFMADD231PS Y10, Y8, Y5
	VMASKMOVPS 24(R8), Y9, Y10
	VFMADD231PS Y10, Y8, Y6
	VMASKMOVPS 28(R8), Y9, Y10
	VFMADD231PS Y10, Y8, Y7

reduce:
	// Compute [0 4] [1 5] [2 6] [3 7] across 128-bit lanes, exactly as
	// libopus: xsumK = add(perm2f128(xsumK,xsum(K+4),0x20),
	//                       perm2f128(xsumK,xsum(K+4),0x31)).
	VPERM2F128 $0x20, Y4, Y0, Y8
	VPERM2F128 $0x31, Y4, Y0, Y9
	VADDPS     Y9, Y8, Y0
	VPERM2F128 $0x20, Y5, Y1, Y8
	VPERM2F128 $0x31, Y5, Y1, Y9
	VADDPS     Y9, Y8, Y1
	VPERM2F128 $0x20, Y6, Y2, Y8
	VPERM2F128 $0x31, Y6, Y2, Y9
	VADDPS     Y9, Y8, Y2
	VPERM2F128 $0x20, Y7, Y3, Y8
	VPERM2F128 $0x31, Y7, Y3, Y9
	VADDPS     Y9, Y8, Y3

	// [0 1 4 5] [2 3 6 7]
	VHADDPS Y1, Y0, Y0
	VHADDPS Y3, Y2, Y1

	// [0 1 2 3 4 5 6 7]
	VHADDPS Y1, Y0, Y0

	VMOVUPS Y0, (DX)
	VZEROUPPER
	RET

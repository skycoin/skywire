// +build amd64
// Code generated by asm2asm, DO NOT EDIT.

package avx2

var _text_vsigned = []byte{
	// .p2align 4, 0x90
	// _vsigned
	0x55, // pushq        %rbp
	0x48, 0x89, 0xe5, //0x00000001 movq         %rsp, %rbp
	0x53, //0x00000004 pushq        %rbx
	0x48, 0x8b, 0x06, //0x00000005 movq         (%rsi), %rax
	0x4c, 0x8b, 0x0f, //0x00000008 movq         (%rdi), %r9
	0x4c, 0x8b, 0x5f, 0x08, //0x0000000b movq         $8(%rdi), %r11
	0x48, 0xc7, 0x02, 0x09, 0x00, 0x00, 0x00, //0x0000000f movq         $9, (%rdx)
	0xc5, 0xf8, 0x57, 0xc0, //0x00000016 vxorps       %xmm0, %xmm0, %xmm0
	0xc5, 0xf8, 0x11, 0x42, 0x08, //0x0000001a vmovups      %xmm0, $8(%rdx)
	0x48, 0x8b, 0x0e, //0x0000001f movq         (%rsi), %rcx
	0x48, 0x89, 0x4a, 0x18, //0x00000022 movq         %rcx, $24(%rdx)
	0x4c, 0x39, 0xd8, //0x00000026 cmpq         %r11, %rax
	0x0f, 0x83, 0x45, 0x00, 0x00, 0x00, //0x00000029 jae          LBB0_1
	0x41, 0x8a, 0x0c, 0x01, //0x0000002f movb         (%r9,%rax), %cl
	0x41, 0xb8, 0x01, 0x00, 0x00, 0x00, //0x00000033 movl         $1, %r8d
	0x80, 0xf9, 0x2d, //0x00000039 cmpb         $45, %cl
	0x0f, 0x85, 0x18, 0x00, 0x00, 0x00, //0x0000003c jne          LBB0_5
	0x48, 0x83, 0xc0, 0x01, //0x00000042 addq         $1, %rax
	0x4c, 0x39, 0xd8, //0x00000046 cmpq         %r11, %rax
	0x0f, 0x83, 0x25, 0x00, 0x00, 0x00, //0x00000049 jae          LBB0_1
	0x41, 0x8a, 0x0c, 0x01, //0x0000004f movb         (%r9,%rax), %cl
	0x49, 0xc7, 0xc0, 0xff, 0xff, 0xff, 0xff, //0x00000053 movq         $-1, %r8
	//0x0000005a LBB0_5
	0x8d, 0x79, 0xc6, //0x0000005a leal         $-58(%rcx), %edi
	0x40, 0x80, 0xff, 0xf5, //0x0000005d cmpb         $-11, %dil
	0x0f, 0x87, 0x1a, 0x00, 0x00, 0x00, //0x00000061 ja           LBB0_7
	0x48, 0x89, 0x06, //0x00000067 movq         %rax, (%rsi)
	0x48, 0xc7, 0x02, 0xfe, 0xff, 0xff, 0xff, //0x0000006a movq         $-2, (%rdx)
	0x5b, //0x00000071 popq         %rbx
	0x5d, //0x00000072 popq         %rbp
	0xc3, //0x00000073 retq         
	//0x00000074 LBB0_1
	0x4c, 0x89, 0x1e, //0x00000074 movq         %r11, (%rsi)
	0x48, 0xc7, 0x02, 0xff, 0xff, 0xff, 0xff, //0x00000077 movq         $-1, (%rdx)
	0x5b, //0x0000007e popq         %rbx
	0x5d, //0x0000007f popq         %rbp
	0xc3, //0x00000080 retq         
	//0x00000081 LBB0_7
	0x80, 0xf9, 0x30, //0x00000081 cmpb         $48, %cl
	0x0f, 0x85, 0x35, 0x00, 0x00, 0x00, //0x00000084 jne          LBB0_12
	0x48, 0x8d, 0x78, 0x01, //0x0000008a leaq         $1(%rax), %rdi
	0x4c, 0x39, 0xd8, //0x0000008e cmpq         %r11, %rax
	0x0f, 0x83, 0x82, 0x00, 0x00, 0x00, //0x00000091 jae          LBB0_11
	0x41, 0x8a, 0x0c, 0x39, //0x00000097 movb         (%r9,%rdi), %cl
	0x80, 0xc1, 0xd2, //0x0000009b addb         $-46, %cl
	0x80, 0xf9, 0x37, //0x0000009e cmpb         $55, %cl
	0x0f, 0x87, 0x72, 0x00, 0x00, 0x00, //0x000000a1 ja           LBB0_11
	0x44, 0x0f, 0xb6, 0xd1, //0x000000a7 movzbl       %cl, %r10d
	0x48, 0xb9, 0x01, 0x00, 0x80, 0x00, 0x00, 0x00, 0x80, 0x00, //0x000000ab movabsq      $36028797027352577, %rcx
	0x4c, 0x0f, 0xa3, 0xd1, //0x000000b5 btq          %r10, %rcx
	0x0f, 0x83, 0x5a, 0x00, 0x00, 0x00, //0x000000b9 jae          LBB0_11
	//0x000000bf LBB0_12
	0x4c, 0x39, 0xd8, //0x000000bf cmpq         %r11, %rax
	0x4d, 0x89, 0xda, //0x000000c2 movq         %r11, %r10
	0x4c, 0x0f, 0x47, 0xd0, //0x000000c5 cmovaq       %rax, %r10
	0x31, 0xc9, //0x000000c9 xorl         %ecx, %ecx
	0x90, 0x90, 0x90, 0x90, 0x90, //0x000000cb .p2align 4, 0x90
	//0x000000d0 LBB0_13
	0x4c, 0x39, 0xd8, //0x000000d0 cmpq         %r11, %rax
	0x0f, 0x83, 0x81, 0x00, 0x00, 0x00, //0x000000d3 jae          LBB0_23
	0x49, 0x0f, 0xbe, 0x3c, 0x01, //0x000000d9 movsbq       (%r9,%rax), %rdi
	0x8d, 0x5f, 0xd0, //0x000000de leal         $-48(%rdi), %ebx
	0x80, 0xfb, 0x09, //0x000000e1 cmpb         $9, %bl
	0x0f, 0x87, 0x35, 0x00, 0x00, 0x00, //0x000000e4 ja           LBB0_18
	0x48, 0x6b, 0xc9, 0x0a, //0x000000ea imulq        $10, %rcx, %rcx
	0x0f, 0x80, 0x14, 0x00, 0x00, 0x00, //0x000000ee jo           LBB0_17
	0x48, 0x83, 0xc0, 0x01, //0x000000f4 addq         $1, %rax
	0x83, 0xc7, 0xd0, //0x000000f8 addl         $-48, %edi
	0x49, 0x0f, 0xaf, 0xf8, //0x000000fb imulq        %r8, %rdi
	0x48, 0x01, 0xf9, //0x000000ff addq         %rdi, %rcx
	0x0f, 0x81, 0xc8, 0xff, 0xff, 0xff, //0x00000102 jno          LBB0_13
	//0x00000108 LBB0_17
	0x48, 0x83, 0xc0, 0xff, //0x00000108 addq         $-1, %rax
	0x48, 0x89, 0x06, //0x0000010c movq         %rax, (%rsi)
	0x48, 0xc7, 0x02, 0xfb, 0xff, 0xff, 0xff, //0x0000010f movq         $-5, (%rdx)
	0x5b, //0x00000116 popq         %rbx
	0x5d, //0x00000117 popq         %rbp
	0xc3, //0x00000118 retq         
	//0x00000119 LBB0_11
	0x48, 0x89, 0x3e, //0x00000119 movq         %rdi, (%rsi)
	0x5b, //0x0000011c popq         %rbx
	0x5d, //0x0000011d popq         %rbp
	0xc3, //0x0000011e retq         
	//0x0000011f LBB0_18
	0x4c, 0x39, 0xd8, //0x0000011f cmpq         %r11, %rax
	0x0f, 0x83, 0x2f, 0x00, 0x00, 0x00, //0x00000122 jae          LBB0_22
	0x41, 0x8a, 0x3c, 0x01, //0x00000128 movb         (%r9,%rax), %dil
	0x40, 0x80, 0xff, 0x2e, //0x0000012c cmpb         $46, %dil
	0x0f, 0x84, 0x14, 0x00, 0x00, 0x00, //0x00000130 je           LBB0_25
	0x40, 0x80, 0xff, 0x45, //0x00000136 cmpb         $69, %dil
	0x0f, 0x84, 0x0a, 0x00, 0x00, 0x00, //0x0000013a je           LBB0_25
	0x40, 0x80, 0xff, 0x65, //0x00000140 cmpb         $101, %dil
	0x0f, 0x85, 0x0d, 0x00, 0x00, 0x00, //0x00000144 jne          LBB0_22
	//0x0000014a LBB0_25
	0x48, 0x89, 0x06, //0x0000014a movq         %rax, (%rsi)
	0x48, 0xc7, 0x02, 0xfa, 0xff, 0xff, 0xff, //0x0000014d movq         $-6, (%rdx)
	0x5b, //0x00000154 popq         %rbx
	0x5d, //0x00000155 popq         %rbp
	0xc3, //0x00000156 retq         
	//0x00000157 LBB0_22
	0x49, 0x89, 0xc2, //0x00000157 movq         %rax, %r10
	//0x0000015a LBB0_23
	0x4c, 0x89, 0x16, //0x0000015a movq         %r10, (%rsi)
	0x48, 0x89, 0x4a, 0x10, //0x0000015d movq         %rcx, $16(%rdx)
	0x5b, //0x00000161 popq         %rbx
	0x5d, //0x00000162 popq         %rbp
	0xc3, //0x00000163 retq         
	//0x00000164 .p2align 2, 0x00
	//0x00000164 _MASK_USE_NUMBER
	0x02, 0x00, 0x00, 0x00, //0x00000164 .long 2
}
 

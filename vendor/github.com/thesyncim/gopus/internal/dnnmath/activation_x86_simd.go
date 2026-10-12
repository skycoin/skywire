//go:build amd64 && goexperiment.simd && !nosimd && !purego

package dnnmath

import (
	"math"
	"simd/archsimd"
)

// X86VectorKernels reports whether the SIMD lane selects libopus's AVX2/FMA
// DNN kernels. The paired libopus build dispatches compute_linear,
// compute_activation and compute_conv2d through dnn/x86/x86_dnn_map.c, whose
// AVX2 slot (nnet_avx2.c, dnn/vec_avx.h with __AVX2__ and __FMA__) is taken
// when celt/x86/x86cpu.c reports AVX, FMA and AVX2.
var X86VectorKernels = archsimd.X86.AVX2() && archsimd.X86.FMA()

// The selected x86 DRED archive uses dnn/vec_avx.h's AVX2/FMA Padé
// polynomial and VRCPPS estimate for both complete vectors and the tail.
// The 256-bit activation bodies clear upper vector registers before
// returning to scalar callers.
// Guard wrappers keep Go 1.27 zero-vector hoisting inside the noinline AVX2
// bodies, after the CPU check (golang/go#81571).
func sigmoidVectorX86(out, in []float32, n int) {
	if !X86VectorKernels {
		SigmoidVectorScalarApprox(out, in, n)
		return
	}
	sigmoidVectorX86AVX2(out, in, n)
}

//go:noinline
func sigmoidVectorX86AVX2(out, in []float32, n int) {
	i := 0
	for ; i+8 <= n; i += 8 {
		sigmoid8X86(archsimd.LoadFloat32x8(in[i:])).Store(out[i:])
	}
	for ; i < n; i++ {
		storeLane0X86(&out[i], sigmoid8X86(archsimd.BroadcastFloat32x8(in[i])))
	}
	archsimd.ClearAVXUpperBits()
}

func tanhVectorX86(out, in []float32, n int) {
	if !X86VectorKernels {
		TanhVectorScalarApprox(out, in, n)
		return
	}
	tanhVectorX86AVX2(out, in, n)
}

//go:noinline
func tanhVectorX86AVX2(out, in []float32, n int) {
	i := 0
	for ; i+8 <= n; i += 8 {
		tanh8X86(archsimd.LoadFloat32x8(in[i:])).Store(out[i:])
	}
	for ; i < n; i++ {
		storeLane0X86(&out[i], tanh8X86(archsimd.BroadcastFloat32x8(in[i])))
	}
	archsimd.ClearAVXUpperBits()
}

func tanhApproxX86(x float32) float32 {
	if !X86VectorKernels {
		return TanhScalarApprox(x)
	}
	return tanhApproxX86AVX2(x)
}

//go:noinline
func tanhApproxX86AVX2(x float32) float32 {
	y := tanh8X86(archsimd.BroadcastFloat32x8(x)).GetLo().GetElem(0)
	archsimd.ClearAVXUpperBits()
	return y
}

func expVectorX86(out, in []float32, n int) {
	if !X86VectorKernels {
		ExpVectorScalarApprox(out, in, n)
		return
	}
	expVectorX86AVX2(out, in, n)
}

//go:noinline
func expVectorX86AVX2(out, in []float32, n int) {
	i := 0
	for ; i+8 <= n; i += 8 {
		exp8X86(archsimd.LoadFloat32x8(in[i:])).Store(out[i:])
	}
	for ; i < n; i++ {
		storeLane0X86(&out[i], exp8X86(archsimd.BroadcastFloat32x8(in[i])))
	}
	archsimd.ClearAVXUpperBits()
}

// storeLane0X86 bit-casts lane 0 before assignment, which stores the scalar
// result without introducing a legacy SSE operation inside the 256-bit region.
func storeLane0X86(dst *float32, v archsimd.Float32x8) {
	*dst = math.Float32frombits(uint32(v.GetLo().AsInt32x4().GetElem(0)))
}

// minPS returns _mm256_min_ps(a, b): MINPS yields its second operand when
// either lane is NaN. archsimd's Min is commutative and leaves that operand
// order to the compiler, so the selection is explicit here.
func minPS(a, b archsimd.Float32x8) archsimd.Float32x8 {
	return a.Merge(b, a.Less(b))
}

// maxPS returns _mm256_max_ps(a, b) with MAXPS's second-operand NaN result.
func maxPS(a, b archsimd.Float32x8) archsimd.Float32x8 {
	return a.Merge(b, a.Greater(b))
}

func exp8X86(x archsimd.Float32x8) archsimd.Float32x8 {
	// dnn/vec_avx.h:exp8_approx scales and clamps before splitting the
	// exponent bits from the FMA-evaluated cubic mantissa.
	scaled := x.Mul(archsimd.BroadcastFloat32x8(1.44269504))
	scaled = maxPS(archsimd.BroadcastFloat32x8(-50), minPS(archsimd.BroadcastFloat32x8(50), scaled))
	integerFloat := scaled.Floor()
	integer := integerFloat.ConvertToInt32()
	frac := scaled.Sub(integerFloat)
	mantissa := archsimd.BroadcastFloat32x8(0.078024523).
		MulAdd(frac, archsimd.BroadcastFloat32x8(0.22606716)).
		MulAdd(frac, archsimd.BroadcastFloat32x8(0.69583354)).
		MulAdd(frac, archsimd.BroadcastFloat32x8(0.99992522))
	bits := integer.ShiftLeft(archsimd.BroadcastUint32x8(23)).Add(mantissa.AsInt32x8())
	return bits.AsFloat32x8()
}

func sigmoid8X86(x archsimd.Float32x8) archsimd.Float32x8 {
	x2 := x.Mul(x)
	num := archsimd.BroadcastFloat32x8(0.00950985).MulAdd(x2, archsimd.BroadcastFloat32x8(6.02452230)).MulAdd(x2, archsimd.BroadcastFloat32x8(238.13200378))
	den := archsimd.BroadcastFloat32x8(0.74287558).MulAdd(x2, archsimd.BroadcastFloat32x8(103.34200287)).MulAdd(x2, archsimd.BroadcastFloat32x8(952.72399902))
	y := num.Mul(x).MulAdd(den.Reciprocal(), archsimd.BroadcastFloat32x8(0.5))
	// A zero vector value, not a broadcast float constant: the constant
	// would be materialized with a legacy XORPS.
	var zero archsimd.Float32x8
	return maxPS(zero, minPS(archsimd.BroadcastFloat32x8(1), y))
}

func tanh8X86(x archsimd.Float32x8) archsimd.Float32x8 {
	x2 := x.Mul(x)
	num := archsimd.BroadcastFloat32x8(0.60863042).MulAdd(x2, archsimd.BroadcastFloat32x8(96.39235687)).MulAdd(x2, archsimd.BroadcastFloat32x8(952.52801514))
	den := archsimd.BroadcastFloat32x8(11.88600922).MulAdd(x2, archsimd.BroadcastFloat32x8(413.36801147)).MulAdd(x2, archsimd.BroadcastFloat32x8(952.72399902))
	y := num.Mul(x).Mul(den.Reciprocal())
	return maxPS(archsimd.BroadcastFloat32x8(-1), minPS(archsimd.BroadcastFloat32x8(1), y))
}

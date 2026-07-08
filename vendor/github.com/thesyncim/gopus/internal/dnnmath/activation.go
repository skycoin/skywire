// Package dnnmath provides the activation, exponent and input-quantization
// kernels libopus' neural-network code (DRED, OSCE) relies on. Each kernel keeps
// a scalar path that mirrors the generic libopus build and, on arm64, a NEON
// path matching the SIMD reference so the DNN output is bit-exact per tier.
package dnnmath

import (
	"math"
	"runtime"

	"github.com/thesyncim/gopus/internal/opusmath"
)

var useNEONApproxActivation = runtime.GOARCH == "arm64"
var useNEONCgemvQuantize = runtime.GOARCH == "arm64"

// SigmoidApprox mirrors libopus' DNN ACTIVATION_SIGMOID path.
func SigmoidApprox(x float32) float32 {
	if useNEONApproxActivation {
		return sigmoidApproxNEON(x)
	}
	return SigmoidScalarApprox(x)
}

// TanhApprox mirrors libopus' DNN ACTIVATION_TANH path.
func TanhApprox(x float32) float32 {
	if useNEONApproxActivation {
		return tanhApproxNEON(x)
	}
	return TanhScalarApprox(x)
}

// SigmoidScalarApprox mirrors libopus' generic DNN sigmoid path.
func SigmoidScalarApprox(x float32) float32 {
	return 0.5 + 0.5*TanhScalarApprox(0.5*x)
}

// TanhScalarApprox mirrors libopus' generic DNN tanh path.
func TanhScalarApprox(x float32) float32 {
	const (
		n0 = 952.52801514
		n1 = 96.39235687
		n2 = 0.60863042
		d0 = 952.72399902
		d1 = 413.36801147
		d2 = 11.88600922
	)
	x2 := x * x
	num := ((n2*x2 + n1) * x2) + n0
	den := ((d2*x2 + d1) * x2) + d0
	y := num * x / den
	if y < -1 {
		return -1
	}
	if y > 1 {
		return 1
	}
	return y
}

// SigmoidVectorApprox mirrors libopus' active vector sigmoid helper, including
// the NEON tail path used when N is not a multiple of four.
func SigmoidVectorApprox(out, in []float32, n int) {
	if useNEONApproxActivation {
		i := 0
		for ; i < n-3; i += 4 {
			out[i] = sigmoidApproxNEON(in[i])
			out[i+1] = sigmoidApproxNEON(in[i+1])
			out[i+2] = sigmoidApproxNEON(in[i+2])
			out[i+3] = sigmoidApproxNEON(in[i+3])
		}
		for ; i < n; i++ {
			out[i] = sigmoidTailNEON(in[i])
		}
		return
	}
	SigmoidVectorScalarApprox(out, in, n)
}

// SigmoidVectorScalarApprox mirrors libopus' generic DNN sigmoid helper.
func SigmoidVectorScalarApprox(out, in []float32, n int) {
	for i := range n {
		out[i] = SigmoidScalarApprox(in[i])
	}
}

// TanhVectorApprox mirrors libopus' active vector tanh helper, including the
// NEON tail path used when N is not a multiple of four.
func TanhVectorApprox(out, in []float32, n int) {
	if useNEONApproxActivation {
		i := 0
		for ; i < n-3; i += 4 {
			out[i] = tanhApproxNEON(in[i])
			out[i+1] = tanhApproxNEON(in[i+1])
			out[i+2] = tanhApproxNEON(in[i+2])
			out[i+3] = tanhApproxNEON(in[i+3])
		}
		for ; i < n; i++ {
			out[i] = tanhTailNEON(in[i])
		}
		return
	}
	TanhVectorScalarApprox(out, in, n)
}

// TanhVectorScalarApprox mirrors libopus' generic DNN tanh helper.
func TanhVectorScalarApprox(out, in []float32, n int) {
	for i := range n {
		out[i] = TanhScalarApprox(in[i])
	}
}

// ExpApprox mirrors libopus' DNN lpcnet_exp() helper used by ACTIVATION_EXP.
func ExpApprox(x float32) float32 {
	return Exp2Approx(x * 1.44269504)
}

// ExpVectorApprox mirrors libopus' active vector exponent kernel. In libopus
// this helper is named softmax(), and ACTIVATION_EXP uses it without the
// normalisation step.
func ExpVectorApprox(out, in []float32, n int) {
	if useNEONApproxActivation {
		i := 0
		for ; i < n-3; i += 4 {
			out[i] = expApproxNEON(in[i])
			out[i+1] = expApproxNEON(in[i+1])
			out[i+2] = expApproxNEON(in[i+2])
			out[i+3] = expApproxNEON(in[i+3])
		}
		for ; i < n; i++ {
			out[i] = expApproxNEON(in[i])
		}
		return
	}
	ExpVectorScalarApprox(out, in, n)
}

// ExpVectorScalarApprox mirrors libopus' generic DNN exponent kernel.
func ExpVectorScalarApprox(out, in []float32, n int) {
	for i := range n {
		out[i] = ExpApprox(in[i])
	}
}

// Exp2Approx mirrors libopus' DNN lpcnet_exp2() cubic approximation.
func Exp2Approx(x float32) float32 {
	integer := int(opusmath.FloorF32ToInt32(x))
	if integer < -50 {
		return 0
	}
	frac := x - float32(integer)
	res := fma32(frac, fma32(frac, fma32(float32(0.078024523), frac, float32(0.22606716)), float32(0.69583354)), float32(0.99992522))
	bits := math.Float32bits(res)
	bits = (bits + uint32(int32(integer)<<23)) & 0x7fffffff
	return math.Float32frombits(bits)
}

// Cgemv8x4QuantizeInput mirrors libopus' cgemv8x4 input quantizer for the
// active DNN vector path. ARM NEON uses nearest-even conversion after a
// float32 multiply; the scalar fallback uses floor(0.5 + 127*x).
func Cgemv8x4QuantizeInput(x float32) int8 {
	if useNEONCgemvQuantize {
		return int8(opusmath.RoundToEvenF32ToInt32(float32(127) * x))
	}
	return Cgemv8x4QuantizeInputScalar(x)
}

// Cgemv8x4QuantizeInputScalar mirrors libopus' generic cgemv8x4 input
// quantizer.
func Cgemv8x4QuantizeInputScalar(x float32) int8 {
	return int8(opusmath.FloorHalfPlusF32ToInt32(float32(127) * x))
}

// SoftmaxApprox mirrors libopus' pinned ACTIVATION_SOFTMAX path. The 1.6.1
// nnet.c build defines SOFTMAX_HACK, so softmax activations copy inputs
// unchanged. ACTIVATION_EXP still uses ExpVectorApprox's exponent kernel.
func SoftmaxApprox(out, in []float32, n int) {
	if n == 0 {
		return
	}
	if len(out) == 0 || len(in) == 0 || &out[0] != &in[0] {
		copy(out[:n], in[:n])
	}
}

// CeltLog mirrors libopus' floating-point celt_log() macro when FLOAT_APPROX
// is enabled in the reference build.
func CeltLog(x float32) float32 {
	return opusmath.CeltLog2(x) * 0.6931471805599453
}

// CeltSin mirrors libopus' floating-point celt_sin() macro.
func CeltSin(x float32) float32 {
	return celtCosNorm2(opusmath.CeltSinNormArg(x))
}

func celtCosNorm2(x float32) float32 {
	x -= float32(4) * float32(opusmath.FloorF32ToInt32(0.25*(x+1)))
	outputSign := float32(1)
	if x > 1 {
		outputSign = -1
		x -= 2
	}
	x2 := x * x
	const (
		a0 = float32(9.999999403953552246093750000000e-01)
		a2 = float32(-1.233698248863220214843750000000000)
		a4 = float32(2.536507546901702880859375000000e-01)
		a6 = float32(-2.08106283098459243774414062500e-02)
		a8 = float32(8.581906440667808055877685546875e-04)
	)
	return outputSign * (a0 + x2*(a2+x2*(a4+x2*(a6+x2*a8))))
}

func sigmoidApproxNEON(x float32) float32 {
	const (
		n0 = float32(238.13200378)
		n1 = float32(6.02452230)
		n2 = float32(0.00950985)
		d0 = float32(952.72399902)
		d1 = float32(103.34200287)
		d2 = float32(0.74287558)
	)
	x2 := x * x
	num := fma32(x2, fma32(n2, x2, n1), n0)
	den := fma32(x2, fma32(d2, x2, d1), d0)
	y := fma32(num*x, reciprocalEstimate32(den), 0.5)
	if y < 0 {
		return 0
	}
	if y > 1 {
		return 1
	}
	return y
}

func sigmoidTailNEON(x float32) float32 {
	ex := expApproxNEON(x)
	return ex / (ex + 1)
}

func tanhApproxNEON(x float32) float32 {
	const (
		n0 = float32(952.52801514)
		n1 = float32(96.39235687)
		n2 = float32(0.60863042)
		d0 = float32(952.72399902)
		d1 = float32(413.36801147)
		d2 = float32(11.88600922)
	)
	x2 := x * x
	num := fma32(x2, fma32(n2, x2, n1), n0)
	den := fma32(x2, fma32(d2, x2, d1), d0)
	y := (num * x) * reciprocalEstimate32(den)
	if y < -1 {
		return -1
	}
	if y > 1 {
		return 1
	}
	return y
}

func tanhTailNEON(x float32) float32 {
	ex2 := expApproxNEON(2 * x)
	return (ex2 - 1) / (ex2 + 1)
}

func expApproxNEON(x float32) float32 {
	if x > 88 {
		x = 88
	} else if x < -88 {
		x = -88
	}
	x = fma32(x, float32(1.44269504), 127)
	integer := int32(x)
	frac := x - float32(integer)
	y := fma32(frac, fma32(frac, fma32(float32(0.078024523), frac, float32(0.22606716)), float32(0.69583354)), float32(0.99992522))
	bits := uint32(integer << 23)
	return y * math.Float32frombits(bits)
}

func fma32(a, b, c float32) float32 {
	return a*b + c
}

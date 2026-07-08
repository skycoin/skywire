// Package opusmath holds the gopus floating-point math kernels shared by the
// CELT, SILK and Opus glue layers. Every function here is a deliberate, narrow
// mirror of a specific libopus 1.6.1 routine or macro from the default
// (non-FIXED_POINT) build, kept in one place so call sites stay bit-exact with
// the reference decoder/encoder.
//
// Width discipline is the whole point of the package:
//
//   - The CELT FLOAT_APPROX approximations (celt_log2, celt_exp2) are reproduced
//     as float32 polynomial evaluations with the exact libopus coefficients and
//     the same bit-twiddling on the IEEE-754 exponent. Doing the arithmetic in
//     float64 would change the rounding and break parity, so these stay in
//     float32 and route their fused multiply-add through fma32 (see fma32_*.go).
//   - The thin SILK/Opus FLP wrappers (in silk.go) wrap libm functions that C
//     evaluates in double and narrows once on assignment to a float. They keep
//     the computation in float64 and convert the single final result, matching
//     that one rounding step. Operands that libopus deliberately keeps in C
//     double use the [CReal] alias.
//   - The PCM quantizers (in pcm.go) reproduce libopus' float-to-int rounding
//     and saturation exactly, including round-half-to-even and the rail clamps.
//
// The package is import-safe from both the default and the gopus_fixed_point
// builds; it contains no build-tagged variants of its own except the fma32
// helper, which is split per architecture only to control FMA contraction.
package opusmath

import "math"

// CeltLog2 mirrors libopus celt_log2() on the FLOAT_APPROX path
// (celt/mathops.h). It decomposes x into an IEEE-754 exponent and mantissa,
// selects one of eight piecewise ranges from the mantissa's top bits, applies a
// degree-4 minimax polynomial to the range-normalised mantissa, and recombines
// with the per-range offset. All arithmetic is float32 with the exact libopus
// coefficients; widening to float64 would change the rounding and lose parity.
func CeltLog2(x float32) float32 {
	bits := math.Float32bits(x)
	integer := int32(bits>>23) - 127
	bits = uint32(int32(bits) - int32(uint32(integer)<<23))

	rangeIdx := (bits >> 20) & 0x7
	f := math.Float32frombits(bits)
	f = f*celtLog2XNormCoeff[rangeIdx] - 1.0625
	f = celtLog2CoeffA0 + f*(celtLog2CoeffA1+f*(celtLog2CoeffA2+f*(celtLog2CoeffA3+f*celtLog2CoeffA4)))
	return float32(integer) + f + celtLog2YNormCoeff[rangeIdx]
}

// CeltExp2 mirrors libopus celt_exp2() on the FLOAT_APPROX path
// (celt/mathops.h). It splits x into integer and fractional parts, evaluates a
// degree-5 polynomial in the fraction via fma32 (so the fused multiply-adds
// match the reference), then adds the integer part straight into the IEEE-754
// exponent field and masks off the sign bit. Inputs below -50 underflow to 0,
// exactly as libopus short-circuits. The float32 width and the fma32 fusion are
// load-bearing for bit-exactness.
func CeltExp2(x float32) float32 {
	integer := int32(math.Floor(float64(x)))
	if integer < -50 {
		return 0
	}
	frac := x - float32(integer)

	res := fma32(frac, fma32(frac, fma32(frac, fma32(frac, fma32(frac,
		celtExp2CoeffA5, celtExp2CoeffA4), celtExp2CoeffA3), celtExp2CoeffA2),
		celtExp2CoeffA1), celtExp2CoeffA0)

	bits := math.Float32bits(res)
	bits = uint32(int32(bits)+int32(uint32(integer)<<23)) & 0x7fffffff
	return math.Float32frombits(bits)
}

// CeltSinNormArg reproduces the argument reduction inside libopus celt_sin()
// (celt/mathops.h): it forms (pi/2)*x - 1 with the half-pi product evaluated in
// double (as the C code does) before narrowing to float, so the reduced argument
// fed to the sine polynomial is bit-exact.
func CeltSinNormArg(x float32) float32 {
	return float32((0.5 * 3.1415926535897931 * float64(x)) - 1)
}

// ISqrt32 returns floor(sqrt(x)), matching libopus isqrt32() (celt/mathops.c).
// It seeds the result from a double sqrt and then corrects up or down with exact
// 64-bit integer multiplies so the floor is exact even where the double sqrt
// rounds the wrong side of an integer boundary. The 64-bit (uint64) products
// avoid overflow for x near 2**32, which the correction loops require.
func ISqrt32(x uint32) uint32 {
	r := uint32(math.Sqrt(float64(x)))
	for uint64(r+1)*uint64(r+1) <= uint64(x) {
		r++
	}
	for uint64(r)*uint64(r) > uint64(x) {
		r--
	}
	return r
}

// celtLog2Coeff* and celtExp2Coeff* are the FLOAT_APPROX polynomial
// coefficients from celt/mathops.h, written out to their full float32 decimal
// expansion so the compiled constants are the same bit patterns libopus uses.
// They must stay float32 (not untyped/float64) constants: rounding each
// coefficient to single precision is part of reproducing the reference output.
const (
	celtLog2CoeffA0 float32 = 8.74628424644470214843750000e-02
	celtLog2CoeffA1 float32 = 1.357829570770263671875000000000
	celtLog2CoeffA2 float32 = -6.3897705078125000000000000e-01
	celtLog2CoeffA3 float32 = 4.01971250772476196289062500e-01
	celtLog2CoeffA4 float32 = -2.8415444493293762207031250e-01

	celtExp2CoeffA0 float32 = 9.999999403953552246093750000000e-01
	celtExp2CoeffA1 float32 = 6.931530833244323730468750000000e-01
	celtExp2CoeffA2 float32 = 2.401536107063293457031250000000e-01
	celtExp2CoeffA3 float32 = 5.582631751894950866699218750000e-02
	celtExp2CoeffA4 float32 = 8.989339694380760192871093750000e-03
	celtExp2CoeffA5 float32 = 1.877576694823801517486572265625e-03
)

// celtLog2XNormCoeff and celtLog2YNormCoeff are the eight-entry per-range
// mantissa scale (1/(1+k/8)) and log2 offset (log2(1+k/8)) tables CeltLog2 uses
// to split the mantissa into piecewise-linear ranges, matching the FLOAT_APPROX
// tables in celt/mathops.h. Index k comes from the mantissa's top three bits.
var celtLog2XNormCoeff = [8]float32{
	1.0000000000000000000000000000,
	8.88888895511627197265625e-01,
	8.00000000000000000000000e-01,
	7.27272748947143554687500e-01,
	6.66666686534881591796875e-01,
	6.15384638309478759765625e-01,
	5.71428596973419189453125e-01,
	5.33333361148834228515625e-01,
}

var celtLog2YNormCoeff = [8]float32{
	0.0000000000000000000000000000,
	1.699250042438507080078125e-01,
	3.219280838966369628906250e-01,
	4.594316184520721435546875e-01,
	5.849624872207641601562500e-01,
	7.004396915435791015625000e-01,
	8.073549270629882812500000e-01,
	9.068905711174011230468750e-01,
}

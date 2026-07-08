package celt

import "math"

// celtLog2 approximates log2(x) using libopus's FLOAT_APPROX polynomial.
// This matches tmp_check/opus-1.6.1/celt/mathops.h (float path).
func celtLog2(x float32) float32 {
	// Libopus assumes x > 0 and does not handle denormals/NaN/inf.
	// Our callers ensure a small epsilon, so keep the same behavior.
	bits := math.Float32bits(x)
	integer := int32(bits>>23) - 127
	// Normalize mantissa to [1, 2) by removing exponent bits.
	bitsInt := int32(bits)
	bitsInt -= int32(uint32(integer) << 23)
	bits = uint32(bitsInt)

	rangeIdx := (bits >> 20) & 0x7
	f := math.Float32frombits(bits)
	f = f*log2XNormCoeff[rangeIdx] - 1.0625

	f = log2CoeffA0 + f*(log2CoeffA1+f*(log2CoeffA2+f*(log2CoeffA3+f*log2CoeffA4)))
	return float32(integer) + f + log2YNormCoeff[rangeIdx]
}

var log2XNormCoeff = [8]float32{
	1.0000000000000000000000000000,
	8.88888895511627197265625e-01,
	8.00000000000000000000000e-01,
	7.27272748947143554687500e-01,
	6.66666686534881591796875e-01,
	6.15384638309478759765625e-01,
	5.71428596973419189453125e-01,
	5.33333361148834228515625e-01,
}

var log2YNormCoeff = [8]float32{
	0.0000000000000000000000000000,
	1.699250042438507080078125e-01,
	3.219280838966369628906250e-01,
	4.594316184520721435546875e-01,
	5.849624872207641601562500e-01,
	7.004396915435791015625000e-01,
	8.073549270629882812500000e-01,
	9.068905711174011230468750e-01,
}

const (
	log2CoeffA0 float32 = 8.74628424644470214843750000e-02
	log2CoeffA1 float32 = 1.357829570770263671875000000000
	log2CoeffA2 float32 = -6.3897705078125000000000000e-01
	log2CoeffA3 float32 = 4.01971250772476196289062500e-01
	log2CoeffA4 float32 = -2.8415444493293762207031250e-01
)

// Package util provides small, dependency-free helpers shared across the gopus
// codec packages. It holds only generic primitives (such as integer/float
// absolute value) that are too trivial to belong to a specific codec stage and
// that several packages would otherwise duplicate.
package util

// Signed constrains the type parameter of [Abs] to the signed integer and
// floating-point kinds gopus operates on. It deliberately matches the width set
// libopus uses internally (signed char / opus_int8, opus_int16, opus_int32,
// opus_int64, plus the float and double sample types), so a single Abs call
// reproduces the C operand width at every call site.
type Signed interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~float32 | ~float64
}

// Abs returns the absolute value of x, mirroring libopus' integer abs() and
// silk_abs_int* / the fabsf() used in the FLP paths.
//
// The width is preserved by the type parameter: |x| is computed as -x in the
// same type as x, so it carries libopus' two's-complement wraparound exactly.
// For the most-negative value of a signed integer type (e.g. math.MinInt16),
// -x is unrepresentable and wraps back to that same negative value, which is
// the identical behaviour of the C unary minus libopus relies on. Callers that
// need a saturating absolute value must clamp before calling Abs.
func Abs[T Signed](x T) T {
	if x < 0 {
		return -x
	}
	return x
}

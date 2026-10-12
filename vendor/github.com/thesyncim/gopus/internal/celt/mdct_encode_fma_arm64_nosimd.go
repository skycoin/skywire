//go:build arm64 && (nosimd || purego)

package celt

import "github.com/thesyncim/gopus/internal/opusmath"

// mdctEncodeFMA32 computes a*b+c for the forward MDCT encoder fold via the
// Go arm64 backend's FMADDS contraction of fma32(a,b,c). The product and sum
// round once to float32, matching the contracted scalar C expression.
func mdctEncodeFMA32(a, b, c float32) float32 { return fma32(a, b, c) }

// mdctMixFMA32 preserves the shared rounding behavior used by ARM64 mix paths.
func mdctMixFMA32(a, b, c float32) float32 { return opusmath.FMA32(a, b, c) }

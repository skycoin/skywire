//go:build (!arm64 && !amd64.v3) || (arm64 && !nosimd && !purego)

package celt

import "github.com/thesyncim/gopus/internal/opusmath"

// mdctEncodeFMA32 uses the shared fused-multiply-add helper. The arm64 nosimd
// implementation uses the Go backend's float32 contraction directly.
func mdctEncodeFMA32(a, b, c float32) float32 { return opusmath.FMA32(a, b, c) }

// mdctMixFMA32 preserves the shared rounding behavior used by existing MDCT
// scalar mix paths outside AMD64 v3.
func mdctMixFMA32(a, b, c float32) float32 { return opusmath.FMA32(a, b, c) }

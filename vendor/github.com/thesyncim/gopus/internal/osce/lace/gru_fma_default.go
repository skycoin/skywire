//go:build !arm64 || nosimd || purego

package lace

// gruFMA32 follows the scalar OSCE compiler's contraction policy: separate
// operations on amd64 and a fused multiply-add on arm64, including nosimd.
// See the arm64 variant for the libopus citation.
func gruFMA32(a, b, c float32) float32 {
	return a*b + c
}

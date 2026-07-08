//go:build !arm64 || purego

package lace

// gruFMA32 is the non-arm64 fallback. On the reference amd64 / purego builds the
// libopus GRU state update is not contracted into a hardware FMA, so the plain
// expression (with the multiply rounding before the add) matches. See the arm64
// variant for the libopus citation.
func gruFMA32(a, b, c float32) float32 {
	return a*b + c
}

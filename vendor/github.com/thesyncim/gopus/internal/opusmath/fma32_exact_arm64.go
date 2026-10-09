//go:build arm64

package opusmath

// FMA32 rounds a*b+c once to float32, matching the libopus arm64 FMADDS
// and NEON FMLA operations. The Go arm64 backend contracts this float32
// expression directly. Keeping the call opaque prevents constant folding
// through a wider intermediate before the float32 rounding.
//
//go:noinline
func FMA32(a, b, c float32) float32 { return a*b + c }

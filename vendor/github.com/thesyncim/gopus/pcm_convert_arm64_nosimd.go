//go:build arm64 && (nosimd || purego || !goexperiment.simd)

package gopus

// The arm64 scalar Go path follows libopus celt_float2int16's 16-sample block
// and scalar-tail rounding. Full blocks use the FCVTNS-equivalent round-to-even
// behavior; FCVTNS matches libopus float2int (lrintf) under the default IEEE
// rounding mode. The arm64 Go SIMD path handles full blocks with archsimd and
// uses the same scalar tail.

const pcmConvertBlock = 16

// fcvtnsFloat32ToInt16 mirrors one lane of FCVTNS followed by SMIN/SQXTN: round
// v*32768 to nearest with ties to even and saturate into int16.
func fcvtnsFloat32ToInt16(v float32) int16 {
	return float32ToInt16(v)
}

func convertFloat32ToInt16Unit(dst []int16, src []float32, n int) bool {
	if n <= 0 {
		return true
	}
	_ = src[n-1]
	_ = dst[n-1]
	blocks := n &^ (pcmConvertBlock - 1)
	for i := 0; i < blocks; i++ {
		v := src[i]
		// Matches FABS + FCMGE(1.0, |v|): out-of-range or NaN samples bail out so
		// the caller's soft-clip fallback reprocesses the whole frame.
		if !(v >= -1 && v <= 1) {
			return false
		}
		dst[i] = fcvtnsFloat32ToInt16(v)
	}
	for i := blocks; i < n; i++ {
		v := src[i]
		if !(v >= -1 && v <= 1) {
			return false
		}
		dst[i] = float32ToInt16(v)
	}
	return true
}

func convertFloat32ToInt16NoSoftClipUnit(dst []int16, src []float32, n int) {
	if n <= 0 {
		return
	}
	_ = src[n-1]
	_ = dst[n-1]
	blocks := n &^ (pcmConvertBlock - 1)
	for i := 0; i < blocks; i++ {
		dst[i] = fcvtnsFloat32ToInt16(src[i])
	}
	for i := blocks; i < n; i++ {
		dst[i] = float32ToInt16(src[i])
	}
}

//go:build arm64 && purego

package gopus

// The non-purego arm64 build converts float32 PCM to int16 with the NEON
// celt_float2int16 kernel (FMUL by 32768, FCVTNS, SMIN, SQXTN) over whole
// 16-sample blocks, then finishes the remainder with the scalar round-to-even
// float32ToInt16. FCVTNS rounds to nearest with ties to even, matching libopus
// float2int (lrintf) under the default IEEE rounding mode. These pure-Go
// fallbacks reproduce the arm64 block/tail split exactly so the purego build
// matches the libopus oracle without hand-written assembly.

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

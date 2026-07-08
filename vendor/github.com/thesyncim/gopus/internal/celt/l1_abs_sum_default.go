//go:build !arm64 || purego

package celt

// celtAbsSumUsesNeon is false off the fused arm64 build, so the float abs-sum
// callers keep the scalar left-to-right reduction and the amd64/purego
// byte-exact gate holds.
const celtAbsSumUsesNeon = false

// l1AbsSumNeon is never called off arm64 (guarded by celtAbsSumUsesNeon).
func l1AbsSumNeon(tmp []float32, n int) float32 {
	var s float32
	for i := 0; i < n && i < len(tmp); i++ {
		v := tmp[i]
		if v < 0 {
			v = -v
		}
		s += v
	}
	return s
}

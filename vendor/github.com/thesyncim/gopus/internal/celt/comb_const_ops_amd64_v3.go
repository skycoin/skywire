//go:build amd64.v3 && !gopus_fixed_point

package celt

// combFilterConstValue follows the scalar libopus constant-body FMA chain.
// The gain products and side-tap sums are rounded before each accumulation.
//
//go:noinline
func combFilterConstValue(base, g10, g11, g12, center, plus1, minus1, plus2, minus2 float32) float32 {
	value := fma32(g10, center, base)
	value = fma32(g11, noFMA32Add(plus1, minus1), value)
	return fma32(g12, noFMA32Add(plus2, minus2), value)
}

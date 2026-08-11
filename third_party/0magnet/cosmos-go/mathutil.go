//go:build js && wasm

package cosmos

import "math"

func clamp(num, min, max float64) float64 {
	return math.Min(math.Max(num, min), max)
}

// linearScale maps [d0,d1] → [r0,r1] (the d3 scaleLinear subset in use).
type linearScale struct {
	d0, d1, r0, r1 float64
}

func (s linearScale) scale(v float64) float64 {
	if s.d1 == s.d0 {
		return (s.r0 + s.r1) / 2
	}
	return s.r0 + (v-s.d0)/(s.d1-s.d0)*(s.r1-s.r0)
}

func extent(values []float64) (min, max float64, ok bool) {
	if len(values) == 0 {
		return 0, 0, false
	}
	min, max = math.Inf(1), math.Inf(-1)
	for _, v := range values {
		if math.IsNaN(v) {
			continue
		}
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if math.IsInf(min, 1) {
		return 0, 0, false
	}
	return min, max, true
}

// mat3 helpers (column-major, gl-matrix layout)

type mat3 [9]float32

func mat3Identity() mat3 {
	return mat3{1, 0, 0, 0, 1, 0, 0, 0, 1}
}

// projection mirrors gl-matrix mat3.projection.
func (m *mat3) projection(width, height float64) {
	*m = mat3{
		float32(2 / width), 0, 0,
		0, float32(-2 / height), 0,
		-1, 1, 1,
	}
}

// translate mirrors gl-matrix mat3.translate (post-multiply).
func (m *mat3) translate(x, y float64) {
	a := *m
	m[6] = float32(x)*a[0] + float32(y)*a[3] + a[6]
	m[7] = float32(x)*a[1] + float32(y)*a[4] + a[7]
	m[8] = float32(x)*a[2] + float32(y)*a[5] + a[8]
}

// scale mirrors gl-matrix mat3.scale (post-multiply).
func (m *mat3) scale(x, y float64) {
	m[0] *= float32(x)
	m[1] *= float32(x)
	m[2] *= float32(x)
	m[3] *= float32(y)
	m[4] *= float32(y)
	m[5] *= float32(y)
}

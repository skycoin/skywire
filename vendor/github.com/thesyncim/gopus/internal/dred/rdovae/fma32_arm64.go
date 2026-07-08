//go:build arm64

package rdovae

func fma32(a, b, c float32) float32 {
	return a*b + c
}

//go:build (!arm64 && !amd64) || (amd64 && purego)

package celt

//go:noinline
func kissMulAddSource(a, b, c, d float32) float32 {
	return a*b + c*d
}

//go:noinline
func kissMulSubSource(a, b, c, d float32) float32 {
	return a*b - c*d
}

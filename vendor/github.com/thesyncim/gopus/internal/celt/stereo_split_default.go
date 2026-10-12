//go:build !amd64 || nosimd || purego || !goexperiment.simd

package celt

// stereoSplitInto runs stereo_split() over x and y of equal length.
func stereoSplitInto(x, y []celtNorm) {
	stereoSplitScalarTarget(x, y)
}

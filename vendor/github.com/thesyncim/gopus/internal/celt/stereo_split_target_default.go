//go:build !amd64.v3 || gopus_fixed_point

package celt

const stereoSplitUsesFMA = false

// stereoSplitScalarTarget preserves the source expression for targets whose
// scalar stereo_split path does not match the AMD64 v3 contraction sequence.
func stereoSplitScalarTarget(x, y []celtNorm) {
	stereoSplitScalar(x, y)
}

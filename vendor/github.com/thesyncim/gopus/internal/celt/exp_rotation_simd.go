//go:build (amd64 || arm64) && goexperiment.simd && !nosimd && !purego

package celt

// expRotationUsesSIMD enables the four-lane spreading-rotation passes for
// stride >= 4 when the host supports the required vector instructions. Each
// lane evaluates exp_rotation1's per-index operations in the scalar order.
var expRotationUsesSIMD = expRotationHostSupportsSIMD()

// expRotation1StrideSIMD is exp_rotation1 for stride >= 4. Four consecutive
// indices then belong to four independent rotation chains: in the forward
// pass index i reads x[i], last written by index i-stride, and the untouched
// x[i+stride], so a block of four needs only earlier blocks. The backward pass
// is the mirror image. Blocks run on four lanes; the remaining indices run in
// the scalar loop, in the same order relative to their chains.
func expRotation1StrideSIMD(x []celtNorm, length, stride int, c, s opusVal16) {
	if !expRotationUsesSIMD {
		expRotation1NormScalar(x, length, stride, c, s)
		return
	}
	c32 := float32(c)
	s32 := float32(s)
	ms32 := -s32

	fwd := length - stride
	blocks := fwd >> 2
	if blocks > 0 {
		expRotation1Pass4(x, 0, stride, blocks, 1, c32, s32)
	}
	for i := blocks * 4; i < fwd; i++ {
		x1 := float32(x[i])
		x2 := float32(x[i+stride])
		x[i+stride] = celtNorm(expRotationMac32(c32, x2, s32, x1))
		x[i] = celtNorm(expRotationMac32(c32, x1, ms32, x2))
	}

	n2 := length - 2*stride
	if n2 <= 0 {
		return
	}
	bblocks := n2 >> 2
	for i := n2 - 1; i >= bblocks*4; i-- {
		x1 := float32(x[i])
		x2 := float32(x[i+stride])
		x[i+stride] = celtNorm(expRotationMac32(c32, x2, s32, x1))
		x[i] = celtNorm(expRotationMac32(c32, x1, ms32, x2))
	}
	if bblocks > 0 {
		expRotation1Pass4(x, (bblocks-1)*4, stride, bblocks, -1, c32, s32)
	}
}

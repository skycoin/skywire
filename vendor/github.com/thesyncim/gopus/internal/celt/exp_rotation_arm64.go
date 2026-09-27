//go:build arm64 && !purego

package celt

// expRotationUsesNeon enables the vectorized spreading-rotation pass on the
// fused arm64 build. The kernel is bit-identical per element to the scalar
// expRotation1Norm ops (TestExpRotation1StrideNeonBitExact); purego keeps the
// scalar loops as the byte-exact oracle.
const expRotationUsesNeon = true

//go:noescape
func expRotation1PassNeon(x []float32, first, stride, blocks, dir int, c, s float32)

// expRotation1StrideNeon runs both expRotation1Norm passes for stride >= 4,
// where four consecutive indices belong to four independent rotation chains.
// The forward pass ascends and the backward pass descends, exactly like the
// scalar loops, so the distance-stride dependencies see their writes in the
// same order; the few indices that do not fill a 4-wide block run scalarly on
// the side of the range that keeps that order (top of the forward pass, top
// first for the backward pass).
func expRotation1StrideNeon(x []celtNorm, length, stride int, c, s opusVal16) {
	c32 := float32(c)
	s32 := float32(s)
	ms32 := -s32

	fwd := length - stride
	blocks := fwd >> 2
	if blocks > 0 {
		expRotation1PassNeon(x, 0, stride, blocks, 1, c32, s32)
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
		expRotation1PassNeon(x, (bblocks-1)*4, stride, bblocks, -1, c32, s32)
	}
}

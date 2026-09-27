//go:build !arm64 || !purego

package celt

// xcorrKernel4Float32Fast delegates to the parity-matched xcorrKernel4Float32
// on every build that isn't arm64+purego. The amd64 default and amd64 purego
// builds are the bit-exact oracles for libopus parity tests
// (TestCELTPVQBandsGridMatchesLibopus, TestCELTEncodeMatchesLibopusC), so they
// must keep the single-chain accumulation order. The arm64 NEON build never
// reaches this kernel (pitchXcorrUsesNeonFMA short-circuits earlier), but the
// stub keeps the quality-gated caller (pitchXCorrFloat32Quality) tag-free.
func xcorrKernel4Float32Fast(x, y []float32, sum *[4]float32, length int) {
	xcorrKernel4Float32(x, y, sum, length)
}

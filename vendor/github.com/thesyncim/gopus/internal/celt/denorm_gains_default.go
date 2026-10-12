//go:build !amd64 || nosimd || purego || !goexperiment.simd

package celt

// denormalizeBandGains sets gains[band] to the denormalise_bands gain
// celt_exp2_db(MIN32(32, bandLogE[band] + eMeans[band])) for band in
// [start, end).
func denormalizeBandGains(gains []float32, energies []celtGLog, start, end int) {
	denormalizeBandGainsScalar(gains, energies, start, end)
}

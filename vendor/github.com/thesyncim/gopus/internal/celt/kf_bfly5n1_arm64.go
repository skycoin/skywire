//go:build arm64 && !purego

package celt

//go:noescape
func kfBfly5N1(fout []kissCpx, tw []kissCpx, m, fstride int)

func useKfBfly5N1(fstride int) bool {
	// On Apple M4 Max, the shipped radix-5 N=1 stages with fstride 4/8 beat the
	// general butterfly, while the hotter fstride 1/2 cases still favor the general
	// arm64 assembly path.
	return fstride >= 4
}

//go:build !amd64.v3 || gopus_fixed_point

package encoder

func stereoWidthDecorrelation(corr opusVal32) opusVal32 {
	return 1 - corr*corr
}

func stereoWidthXYUpdate(beta, oldXY, roundedAlphaXY opusVal32) opusVal32 {
	return fma32(beta, oldXY, roundedAlphaXY)
}

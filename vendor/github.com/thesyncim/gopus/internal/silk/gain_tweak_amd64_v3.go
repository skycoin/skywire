//go:build amd64.v3 && !gopus_fixed_point

package silk

func silkGainTweak32(gain, gainMult, gainAdd float32) float32 {
	return gain*gainMult + gainAdd
}

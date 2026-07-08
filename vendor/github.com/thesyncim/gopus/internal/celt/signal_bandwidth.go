package celt

// celtMinSignalBandwidth mirrors libopus celt_encoder.c min_bandwidth selection.
func celtMinSignalBandwidth(equivRate, channels int) int {
	if equivRate < 32000*channels {
		return 13
	}
	if equivRate < 48000*channels {
		return 16
	}
	if equivRate < 60000*channels {
		return 18
	}
	if equivRate < 80000*channels {
		return 19
	}
	return 20
}

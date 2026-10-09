//go:build gopus_qext

package encoder

import "github.com/thesyncim/gopus/internal/celt"

const hd96kQEXTPacketSizeCap = 3825

func configureCELTEncoderForSampleRate(enc *celt.Encoder, sampleRate int32) {
	if sampleRate == 96000 {
		enc.EnableHD96kMode()
		enc.SetTopLevelDelayCompensatedInput(true)
	}
}

//go:build gopus_dred || gopus_osce

package celt

func (d *Decoder) lastPLCFrameWasNeural() bool {
	if d == nil {
		return false
	}
	return plcFrameIsNeural(int(d.plcLastFrameType))
}

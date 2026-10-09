//go:build gopus_fixed_point

package multistream

import "github.com/thesyncim/gopus/internal/fixedpoint"

// decodeDegenerateToResFixed follows opus_decode_frame's len<=1 concealment
// branch. The packet TOC controls duration; the preceding decoded mode controls
// synthesis, and an uninitialized stream emits zeros.
func (d *streamState) decodeDegenerateToResFixed(data []byte, frameSize int) ([]int32, bool, error) {
	d.beginFixedHybridPLCCapture(frameSize)
	floatPCM, err := d.decodePacketToFloat32Unscaled(data, frameSize)
	d.endFixedHybridPLCCapture()
	if err != nil {
		return nil, false, err
	}
	needed := frameSize * int(d.channels)
	if !d.haveDecoded {
		if cap(d.fixedRes) < needed {
			d.fixedRes = make([]int32, needed)
		}
		d.fixedRes = d.fixedRes[:needed]
		clear(d.fixedRes)
		return d.fixedRes, true, nil
	}
	res, err := d.decodeLostFixed(frameSize, floatPCM)
	if err != nil {
		return nil, false, err
	}
	if d.decodeGainQ8 != 0 {
		fixedpoint.ApplyDecodeGainRes(res, fixedpoint.DecodeGainQ16(int(d.decodeGainQ8)))
	}
	return res, true, nil
}

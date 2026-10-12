//go:build gopus_fixed_point

package gopus

import "github.com/thesyncim/gopus/internal/fixedpoint"

const fixedResToFloat32Scale = float32(1.0 / 8388608.0)

// fixedDecodePLCFloat32 attempts CELT-only multistream PLC in opus_res and
// converts the mapped result with RES2FLOAT semantics.
func (d *MultistreamDecoder) fixedDecodePLCFloat32(pcm []float32, frameSize int) (bool, error) {
	res, handled, err := d.dec.DecodePLCToResFixed(frameSize)
	if err != nil || !handled {
		return handled, err
	}
	channels := int(d.channels)
	if channels <= 0 || len(res)%channels != 0 {
		return false, ErrInvalidFrameSize
	}
	if len(res) > len(pcm) {
		return false, ErrBufferTooSmall
	}
	for i, sample := range res {
		pcm[i] = float32(sample) * fixedResToFloat32Scale
	}
	return true, nil
}

// fixedDecodeInt16 attempts the FIXED_POINT integer multistream decode for an
// int16-output packet. It routes each elementary stream through the integer
// opus_res path and applies the surround channel mapping in the integer domain,
// then converts each output sample with RES2INT16, matching
// opus_multistream_decode built FIXED_POINT (copy_channel_out_short, no soft
// clip). It returns handled=true only when every stream frame was produced
// bit-exactly by the integer path; otherwise the caller uses the float
// conversion for this packet.
func (d *MultistreamDecoder) fixedDecodeInt16(data []byte, pcm []int16, frameSize int) (bool, error) {
	res, allHandled, err := d.dec.DecodeToResFixed(data, frameSize)
	if err != nil {
		return false, err
	}
	if res == nil || !allHandled {
		return false, nil
	}
	channels := int(d.channels)
	needed := frameSize * channels
	if len(res) < needed || len(pcm) < needed {
		return false, nil
	}
	for i := 0; i < needed; i++ {
		pcm[i] = fixedpoint.Res2Int16(res[i])
	}
	return true, nil
}

// fixedDecodeInt24 attempts the FIXED_POINT integer multistream decode for an
// int24-output packet. For the ENABLE_RES24 build RES2INT24(a) == a, so the
// opus_res sample is the int24 PCM value, matching opus_multistream_decode24
// (copy_channel_out_int24). Returns handled=true only when every stream frame
// was produced bit-exactly by the integer path.
func (d *MultistreamDecoder) fixedDecodeInt24(data []byte, pcm []int32, frameSize int) (bool, error) {
	res, allHandled, err := d.dec.DecodeToResFixed(data, frameSize)
	if err != nil {
		return false, err
	}
	if res == nil || !allHandled {
		return false, nil
	}
	channels := int(d.channels)
	needed := frameSize * channels
	if len(res) < needed || len(pcm) < needed {
		return false, nil
	}
	copy(pcm[:needed], res[:needed])
	return true, nil
}

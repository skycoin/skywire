//go:build gopus_fixed_point

package silk

import "github.com/thesyncim/gopus/internal/rangecoding"

// EncodeResQ8 is silk_Encode for the selected FIXED_POINT+ENABLE_RES24
// frontend. samplesIn contains interleaved opus_res values in Q8, and
// enc_API.c applies RES2INT16 at the same points as the C implementation.
func (s *PacketEncoder) EncodeResQ8(ctl *EncControl, samplesIn []int32, nSamplesIn int, re *rangecoding.Encoder, prefill, activity int) (int32, error) {
	if len(samplesIn) != nSamplesIn*int(ctl.NChannelsAPI) {
		return 0, ErrInvalidSampleCount
	}
	return s.encode(ctl, nil, samplesIn, nSamplesIn, re, prefill, activity)
}

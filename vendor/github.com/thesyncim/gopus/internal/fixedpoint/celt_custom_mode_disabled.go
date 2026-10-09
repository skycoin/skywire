//go:build gopus_fixed_point && !gopus_custom_modes

package fixedpoint

import "github.com/thesyncim/gopus/internal/rangecoding"

func (e *CELTEncoder) quantAllBandsCustom(_ *rangecoding.Encoder, _, _, _, _, _ int,
	_, _, _, _, _ []int32, _, _, _, _, _ int, _ *uint32, _ *celtEncodeScratch) []byte {
	return nil
}

func (d *CELTDecoder) quantAllBandsCustom(_ *rangecoding.Decoder, _, _, _, _, _ int,
	_, _ []int32, _, _, _, _, _, _, _ int,
	_ *uint32) (left, right []int32, collapse []byte) {
	return nil, nil, nil
}

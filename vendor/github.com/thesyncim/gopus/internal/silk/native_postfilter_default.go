//go:build !gopus_osce

package silk

const nativePostfilterEnabled = false

type nativePostfilterExtras struct{}

func (d *Decoder) fireNativePostfilterHook(_ int, _ *decoderState, _ *decoderControl, _ []int16) bool {
	return false
}

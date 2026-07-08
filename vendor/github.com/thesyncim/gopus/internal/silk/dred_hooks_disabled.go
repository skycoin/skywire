//go:build !gopus_dred && !gopus_osce

package silk

const (
	dredHooksEnabled            = false
	nativeLowbandCaptureEnabled = false
)

type dredHookState struct{}

// SetRawMonoFrameHook is a no-op in the default build; the raw mono frame hook
// is only wired under the gopus_dred or gopus_osce tags.
func (d *Decoder) SetRawMonoFrameHook(_ RawMonoFrameHook) {}

// SetDeepPLCLossMonoHook is a no-op in the default build; the deep-PLC loss hook
// is only wired under the gopus_dred or gopus_osce tags.
func (d *Decoder) SetDeepPLCLossMonoHook(_ DeepPLCLossMonoHook) {}

func (d *Decoder) fireRawMonoFrameHook(_ int, _ *decoderState, _ []int16) {}

func (d *Decoder) hasDeepPLCLossMonoHook() bool {
	return false
}

func (d *Decoder) fireDeepPLCLossMonoHook(_ []float32) (bool, int) {
	return false, 0
}

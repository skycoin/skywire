//go:build gopus_dred || gopus_osce

package silk

const (
	dredHooksEnabled            = true
	nativeLowbandCaptureEnabled = true
)

type dredHookState struct {
	rawMonoFrameHook     RawMonoFrameHook
	rawMonoLossFrameHook RawMonoFrameHook
	deepPLCLossMonoHook  DeepPLCLossMonoHook
}

// SetRawMonoFrameHook installs a callback fired on raw mono/mid 10 ms chunks
// before CNG/glue. Pass nil to disable.
func (d *Decoder) SetRawMonoFrameHook(hook RawMonoFrameHook) {
	if d == nil {
		return
	}
	d.rawMonoFrameHook = hook
}

// SetRawMonoLossFrameHook installs the raw mono/mid callback for concealed
// SILK frames. It is separate because libopus updates LPCNet history for
// concealed frames only after the PLC model is loaded.
func (d *Decoder) SetRawMonoLossFrameHook(hook RawMonoFrameHook) {
	if d == nil {
		return
	}
	d.rawMonoLossFrameHook = hook
}

// SetDeepPLCLossMonoHook installs a mono 16 kHz PLC concealment hook used by
// optional deep-PLC/DRED experiments. Pass nil to disable.
func (d *Decoder) SetDeepPLCLossMonoHook(hook DeepPLCLossMonoHook) {
	if d == nil {
		return
	}
	d.deepPLCLossMonoHook = hook
}

func (d *Decoder) fireRawMonoFrameHook(channel int, st *decoderState, frameOut []int16) {
	if d == nil {
		return
	}
	d.fireRawMonoFrameHookWith(d.rawMonoFrameHook, channel, st, frameOut)
}

func (d *Decoder) fireRawMonoLossFrameHook(channel int, st *decoderState, frameOut []int16) {
	if d == nil {
		return
	}
	d.fireRawMonoFrameHookWith(d.rawMonoLossFrameHook, channel, st, frameOut)
}

func (d *Decoder) fireRawMonoFrameHookWith(hook RawMonoFrameHook, channel int, st *decoderState, frameOut []int16) {
	if d == nil || hook == nil || channel != 0 || st == nil || st.fsKHz != 16 || st.subfrLength <= 0 {
		return
	}
	chunkSamples := 2 * int(st.subfrLength)
	if chunkSamples <= 0 || len(frameOut) < chunkSamples {
		return
	}
	for offset := 0; offset+chunkSamples <= len(frameOut); offset += chunkSamples {
		hook(frameOut[offset : offset+chunkSamples])
	}
}

func (d *Decoder) hasDeepPLCLossMonoHook() bool {
	return d != nil && d.deepPLCLossMonoHook != nil
}

func (d *Decoder) fireDeepPLCLossMonoHook(concealed []float32) (bool, int) {
	if !d.hasDeepPLCLossMonoHook() {
		return false, 0
	}
	return d.deepPLCLossMonoHook(concealed)
}

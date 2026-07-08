//go:build gopus_osce

package silk

const nativePostfilterEnabled = true

// NativePostfilterHook is invoked once per decoded frame with the channel index,
// the int16 output samples (modifiable in place) and the latest decoder control
// parameters. Returning true signals that the hook handled post-filtering.
type NativePostfilterHook func(channel int, samples []int16, ctrl LatestDecoderControl) bool

type nativePostfilterExtras struct {
	hook NativePostfilterHook
}

// SetNativePostfilterHook installs the per-frame native post-filter callback;
// available only under the gopus_osce tag. Pass nil to disable.
func (d *Decoder) SetNativePostfilterHook(hook NativePostfilterHook) {
	d.nativePostfilter.hook = hook
}

func latestDecoderControlFromFrame(st *decoderState, ctrl *decoderControl) LatestDecoderControl {
	if st == nil || ctrl == nil {
		return LatestDecoderControl{}
	}
	return LatestDecoderControl{
		PredCoefQ12: ctrl.PredCoefQ12,
		LTPCoefQ14:  ctrl.LTPCoefQ14,
		GainsQ16:    ctrl.GainsQ16,
		PitchL:      ctrl.pitchL,
		SignalType:  int32(st.indices.signalType),
		LPCOrder:    st.lpcOrder,
		NbSubfr:     st.nbSubfr,
		FsKHz:       st.fsKHz,
		NumBits:     ctrl.NumBits,
	}
}

func (d *Decoder) fireNativePostfilterHook(channel int, st *decoderState, ctrl *decoderControl, frameOut []int16) bool {
	if d == nil || d.nativePostfilter.hook == nil {
		return false
	}
	return d.nativePostfilter.hook(channel, frameOut, latestDecoderControlFromFrame(st, ctrl))
}

package silk

import "github.com/thesyncim/gopus/internal/plc"

func pitchLagFromState(state *plc.SILKPLCState, st *decoderState) int {
	if state != nil {
		return int((state.PitchLQ8 + 128) >> 8)
	}
	if st == nil {
		return 0
	}
	return int(st.lagPrev)
}

func lastGainQ16FromState(st *decoderState) int32 {
	if st == nil {
		return 1 << 16
	}
	return st.prevGainQ16
}

func excitationHistoryFromState(st *decoderState) []int32 {
	if st == nil {
		return nil
	}
	frameLength := int(st.frameLength)
	if frameLength > 0 && frameLength <= len(st.excQ14) {
		return st.excQ14[:frameLength]
	}
	return st.excQ14[:]
}

func plcLPCCoefficientsQ12(state *plc.SILKPLCState) []int16 {
	if state == nil {
		return nil
	}
	order := min(max(int(state.LPCOrder), 0), maxLPCOrder)
	return state.PrevLPCQ12[:order]
}

func sampleRateKHzFromState(st *decoderState) int {
	if st == nil || st.fsKHz <= 0 {
		return 16
	}
	return int(st.fsKHz)
}

func subframeLengthFromState(st *decoderState) int {
	if st == nil || st.subfrLength <= 0 {
		return 80
	}
	return int(st.subfrLength)
}

func numSubframesFromState(st *decoderState) int {
	if st == nil || st.nbSubfr <= 0 {
		return maxNbSubfr
	}
	return int(st.nbSubfr)
}

func ltpMemoryLengthFromState(st *decoderState) int {
	if st == nil || st.ltpMemLength <= 0 {
		return maxFrameLength
	}
	return int(st.ltpMemLength)
}

func slpcQ14HistoryFromState(st *decoderState) []int32 {
	if st == nil {
		return nil
	}
	order := int(st.lpcOrder)
	if order <= 0 {
		return nil
	}
	if order > maxLPCOrder {
		order = maxLPCOrder
	}
	start := max(maxLPCOrder-order, 0)
	return st.sLPCQ14Buf[start:maxLPCOrder]
}

func outBufHistoryFromState(st *decoderState) []int16 {
	if st == nil {
		return nil
	}
	mem := int(st.ltpMemLength)
	if mem <= 0 {
		return nil
	}
	if mem > len(st.outBuf) {
		mem = len(st.outBuf)
	}
	return st.outBuf[:mem]
}

func lpcOrderFromState(st *decoderState) int {
	if st == nil {
		return 0
	}
	if st.lpcOrder > 0 {
		return int(st.lpcOrder)
	}
	return 16
}

func signalTypeFromState(st *decoderState) int {
	if st == nil {
		return 0
	}
	return int(st.indices.signalType)
}

// silkPLCChannelView adapts one channel of a Decoder to the
// plc.SILKDecoderStateExtended interface consumed by ConcealSILKWithLTP. It
// holds only the decoder pointer and an immutable channel index; the GetX
// accessors below read the live per-channel decoder state on demand so the PLC
// concealment path observes the most recent decoded frame without copying.
type silkPLCChannelView struct {
	d  *Decoder
	ch int
}

func (d *Decoder) plcDecoderView(channel int) *silkPLCChannelView {
	if channel < 0 || channel >= len(d.state) {
		return nil
	}
	// The view only wraps the decoder and an immutable channel index, so it is
	// cached per channel to keep the PLC concealment path allocation-free.
	if channel >= len(d.plcViews) {
		return &silkPLCChannelView{d: d, ch: channel}
	}
	if d.plcViews[channel] == nil {
		d.plcViews[channel] = &silkPLCChannelView{d: d, ch: channel}
	}
	return d.plcViews[channel]
}

func (v *silkPLCChannelView) state() *decoderState {
	if v == nil || v.d == nil || v.ch < 0 || v.ch >= len(v.d.state) {
		return nil
	}
	return &v.d.state[v.ch]
}

func (v *silkPLCChannelView) PrevLPCValues() []float32 {
	return v.d.prevLPCValues
}

func (v *silkPLCChannelView) LPCOrder() int {
	return lpcOrderFromState(v.state())
}

func (v *silkPLCChannelView) IsPreviousFrameVoiced() bool {
	return signalTypeFromState(v.state()) == typeVoiced
}

func (v *silkPLCChannelView) OutputHistory() []float32 {
	return v.d.outputHistory
}

func (v *silkPLCChannelView) HistoryIndex() int {
	return v.d.historyIndex
}

func (v *silkPLCChannelView) GetLastSignalType() int {
	return signalTypeFromState(v.state())
}

func (v *silkPLCChannelView) GetLTPCoefficients() [ltpOrder]int16 {
	state := v.d.ensureSILKPLCState(v.ch)
	if state == nil {
		return [ltpOrder]int16{}
	}
	return state.LTPCoefQ14
}

func (v *silkPLCChannelView) GetPitchLag() int {
	return pitchLagFromState(v.d.ensureSILKPLCState(v.ch), v.state())
}

func (v *silkPLCChannelView) GetLastGain() int32 {
	return lastGainQ16FromState(v.state())
}

func (v *silkPLCChannelView) GetLTPScale() int32 {
	state := v.d.ensureSILKPLCState(v.ch)
	if state == nil {
		return 0
	}
	return state.PrevLTPScaleQ14
}

func (v *silkPLCChannelView) GetExcitationHistory() []int32 {
	return excitationHistoryFromState(v.state())
}

func (v *silkPLCChannelView) GetLPCCoefficientsQ12() []int16 {
	return plcLPCCoefficientsQ12(v.d.ensureSILKPLCState(v.ch))
}

func (v *silkPLCChannelView) GetSampleRateKHz() int {
	return sampleRateKHzFromState(v.state())
}

func (v *silkPLCChannelView) GetSubframeLength() int {
	return subframeLengthFromState(v.state())
}

func (v *silkPLCChannelView) GetNumSubframes() int {
	return numSubframesFromState(v.state())
}

func (v *silkPLCChannelView) GetLTPMemoryLength() int {
	return ltpMemoryLengthFromState(v.state())
}

func (v *silkPLCChannelView) GetSLPCQ14HistoryQ14() []int32 {
	return slpcQ14HistoryFromState(v.state())
}

func (v *silkPLCChannelView) SetSLPCQ14HistoryQ14(history []int32) {
	setSLPCQ14HistoryQ14(v.state(), history)
}

func (v *silkPLCChannelView) GetOutBufHistoryQ0() []int16 {
	return outBufHistoryFromState(v.state())
}

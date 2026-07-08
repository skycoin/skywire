// This file bridges the SILK decoder state to the plc package. libopus runs
// packet-loss concealment by reading fields directly out of silk_decoder_state
// (silk/PLC.c); here those reads go through the accessors below so the plc
// package does not depend on the unexported decoderState layout. The Get* and
// Set* methods all operate on channel 0 (the mono / mid channel), which is the
// only channel libopus feeds into decoder-side concealment.

package silk

import "github.com/thesyncim/gopus/internal/plc"

// ensureSILKPLCState lazily creates and returns the per-channel SILK PLC state,
// or nil for an out-of-range channel. Mirrors the per-channel sPLC member of
// libopus silk_decoder_state (silk/structs.h, used by silk/PLC.c).
func (d *Decoder) ensureSILKPLCState(channel int) *plc.SILKPLCState {
	if channel < 0 || channel >= len(d.silkPLCState) {
		return nil
	}
	if d.silkPLCState[channel] == nil {
		d.silkPLCState[channel] = plc.NewSILKPLCState()
	}
	return d.silkPLCState[channel]
}

// updateSILKPLCStateFromCtrl snapshots the parameters of a successfully decoded
// frame (signal type, pitch lags, LTP coefficients and scale, gains, LPC
// coefficients and rate) into the channel's PLC state so the next lost frame can
// be concealed. Mirrors the silk_PLC_update call made after a good frame in
// libopus silk/decode_frame.c.
func (d *Decoder) updateSILKPLCStateFromCtrl(channel int, st *decoderState, ctrl *decoderControl) {
	if st == nil || ctrl == nil {
		return
	}
	state := d.ensureSILKPLCState(channel)
	if state == nil {
		return
	}

	nbSubfr := int(st.nbSubfr)
	if nbSubfr <= 0 || nbSubfr > maxNbSubfr {
		nbSubfr = maxNbSubfr
	}
	lpcOrder := int(st.lpcOrder)
	if lpcOrder <= 0 || lpcOrder > maxLPCOrder {
		lpcOrder = maxLPCOrder
	}

	fsKHz := int(st.fsKHz)
	if fsKHz <= 0 {
		fsKHz = 16
	}
	subfrLength := int(st.subfrLength)
	if subfrLength <= 0 {
		subfrLength = 80
	}

	var pitchL [maxNbSubfr]int32
	copy(pitchL[:nbSubfr], ctrl.pitchL[:nbSubfr])

	var ltpCoefQ14 [ltpOrder * maxNbSubfr]int16
	copy(ltpCoefQ14[:nbSubfr*ltpOrder], ctrl.LTPCoefQ14[:nbSubfr*ltpOrder])

	var gainsQ16 [maxNbSubfr]int32
	copy(gainsQ16[:nbSubfr], ctrl.GainsQ16[:nbSubfr])

	var lpcQ12 [maxLPCOrder]int16
	copy(lpcQ12[:lpcOrder], ctrl.PredCoefQ12[1][:lpcOrder])

	state.UpdateFromGoodFrame(
		int(st.indices.signalType),
		pitchL[:nbSubfr],
		ltpCoefQ14[:nbSubfr*ltpOrder],
		ctrl.LTPScaleQ14,
		gainsQ16[:nbSubfr],
		lpcQ12[:lpcOrder],
		fsKHz,
		nbSubfr,
		subfrLength,
	)
	state.LastFrameLost = false
}

// GetLTPCoefficients returns the channel-0 PLC state's last long-term-prediction
// filter coefficients (Q14), used to seed pitch-based concealment.
func (d *Decoder) GetLTPCoefficients() [ltpOrder]int16 {
	state := d.ensureSILKPLCState(0)
	if state == nil {
		return [ltpOrder]int16{}
	}
	return state.LTPCoefQ14
}

// GetPitchLag returns the channel-0 pitch lag (samples) carried for concealment.
func (d *Decoder) GetPitchLag() int {
	return pitchLagFromState(d.ensureSILKPLCState(0), &d.state[0])
}

// GetLastGain returns the channel-0 most recent subframe gain (Q16).
func (d *Decoder) GetLastGain() int32 {
	return lastGainQ16FromState(&d.state[0])
}

// GetLTPScale returns the channel-0 PLC state's last LTP scaling factor (Q14).
func (d *Decoder) GetLTPScale() int32 {
	state := d.ensureSILKPLCState(0)
	if state == nil {
		return 0
	}
	return state.PrevLTPScaleQ14
}

// GetExcitationHistory returns the channel-0 decoder's most recent excitation
// (Q14), the lookback PLC uses to extrapolate a concealed frame.
func (d *Decoder) GetExcitationHistory() []int32 {
	return excitationHistoryFromState(&d.state[0])
}

// GetLPCCoefficientsQ12 returns the channel-0 PLC state's last LPC short-term
// prediction coefficients (Q12).
func (d *Decoder) GetLPCCoefficientsQ12() []int16 {
	return plcLPCCoefficientsQ12(d.ensureSILKPLCState(0))
}

// GetSampleRateKHz returns the channel-0 internal SILK sample rate in kHz.
func (d *Decoder) GetSampleRateKHz() int {
	return sampleRateKHzFromState(&d.state[0])
}

// GetSubframeLength returns the channel-0 subframe length in samples.
func (d *Decoder) GetSubframeLength() int {
	return subframeLengthFromState(&d.state[0])
}

// GetNumSubframes returns the channel-0 number of subframes for the last frame.
func (d *Decoder) GetNumSubframes() int {
	return numSubframesFromState(&d.state[0])
}

// GetLTPMemoryLength returns the channel-0 LTP memory length in samples.
func (d *Decoder) GetLTPMemoryLength() int {
	return ltpMemoryLengthFromState(&d.state[0])
}

// GetSLPCQ14HistoryQ14 returns a copy of the channel-0 LPC synthesis filter
// state (sLPC_Q14), the order samples of history the next frame filters from.
func (d *Decoder) GetSLPCQ14HistoryQ14() []int32 {
	return slpcQ14HistoryFromState(&d.state[0])
}

// setSLPCQ14HistoryQ14 writes the trailing order samples of history into a
// channel's LPC synthesis filter state (sLPC_Q14), so a concealment path can
// restore continuity before the next decoded frame.
func setSLPCQ14HistoryQ14(st *decoderState, history []int32) {
	if st == nil || len(history) == 0 {
		return
	}
	order := int(st.lpcOrder)
	if order <= 0 {
		return
	}
	if order > maxLPCOrder {
		order = maxLPCOrder
	}
	if len(history) < order {
		order = len(history)
	}
	start := max(maxLPCOrder-order, 0)
	copy(st.sLPCQ14Buf[start:maxLPCOrder], history[len(history)-order:])
}

// SetSLPCQ14HistoryQ14 restores the channel-0 LPC synthesis filter state
// (sLPC_Q14) from the trailing samples of history.
func (d *Decoder) SetSLPCQ14HistoryQ14(history []int32) {
	setSLPCQ14HistoryQ14(&d.state[0], history)
}

// GetOutBufHistoryQ0 returns the channel-0 decoder output history buffer
// (out_buf, Q0 int16), the past output PLC and LTP read back from.
func (d *Decoder) GetOutBufHistoryQ0() []int16 {
	return outBufHistoryFromState(&d.state[0])
}

// FillMonoOutBufTailFloat copies the most recent samples of the channel-0 output
// history into dst as float32 in [-1, 1), returning the number written (0 if the
// request cannot be satisfied). Used to prime concealment from past output.
func (d *Decoder) FillMonoOutBufTailFloat(dst []float32, samples int) int {
	if d == nil || samples <= 0 || len(dst) < samples {
		return 0
	}
	history := outBufHistoryFromState(&d.state[0])
	if len(history) < samples {
		return 0
	}
	history = history[len(history)-samples:]
	const scale = float32(1.0 / 32768.0)
	for i := range samples {
		dst[i] = float32(history[i]) * scale
	}
	return samples
}

//go:build gopus_dred || gopus_osce

package silk

// DecoderStateSnapshot captures the per-channel SILK decoder state needed to
// save and restore a decoder around DRED/deep-PLC concealment, exposed only
// under the gopus_dred and gopus_osce tags.
type DecoderStateSnapshot struct {
	LagPrev        int32
	LastGainIndex  int
	LossCount      int
	PrevSignalType int
	SMid           [2]float32
	OutBuf         [maxFrameLength + 2*maxSubFrameLength]float32
	SLPCQ14        [maxLPCOrder]float32
	ExcQ14         [maxFrameLength]float32
	ResamplerIIR   [6]float32
	ResamplerFIR   [8]float32
	ResamplerDelay [96]float32
}

// SnapshotDecoderState returns a DecoderStateSnapshot for the given channel,
// converting the live fixed-point decoder state to the float representation used
// by the snapshot. It returns the zero snapshot for a nil decoder or an
// out-of-range channel.
func (d *Decoder) SnapshotDecoderState(bandwidth Bandwidth, channel int) DecoderStateSnapshot {
	if d == nil || channel < 0 || channel >= len(d.state) {
		return DecoderStateSnapshot{}
	}
	st := &d.state[channel]
	snap := DecoderStateSnapshot{
		LagPrev:        st.lagPrev,
		LastGainIndex:  int(st.lastGainIndex),
		LossCount:      int(st.lossCnt),
		PrevSignalType: int(st.prevSignalType),
		SMid: [2]float32{
			float32(d.stereo.sMid[0]) * (1.0 / 32768.0),
			float32(d.stereo.sMid[1]) * (1.0 / 32768.0),
		},
	}
	for i := range st.outBuf {
		snap.OutBuf[i] = float32(st.outBuf[i]) * (1.0 / 32768.0)
	}
	for i := range st.sLPCQ14Buf {
		snap.SLPCQ14[i] = float32(st.sLPCQ14Buf[i])
	}
	for i := range st.excQ14 {
		snap.ExcQ14[i] = float32(st.excQ14[i])
	}
	var resampler *LibopusResampler
	if d.resamplers != nil {
		if pair := d.resamplers[bandwidth]; pair != nil {
			if channel == 1 {
				resampler = pair.right
			} else {
				resampler = pair.left
			}
		}
	}
	if resampler == nil {
		return snap
	}
	if resampler.down != nil {
		down := resampler.down.State()
		for i := range down.sIIR {
			if i >= len(snap.ResamplerIIR) {
				break
			}
			snap.ResamplerIIR[i] = float32(down.sIIR[i])
		}
		for i := range snap.ResamplerFIR {
			src := i >> 1
			if src >= len(down.sFIR) {
				break
			}
			v := down.sFIR[src]
			if i&1 != 0 {
				v >>= 16
			}
			snap.ResamplerFIR[i] = float32(int16(v)) * (1.0 / 32768.0)
		}
		for i := range down.delayBuf {
			if i >= len(snap.ResamplerDelay) {
				break
			}
			snap.ResamplerDelay[i] = float32(down.delayBuf[i]) * (1.0 / 32768.0)
		}
		return snap
	}
	for i := range resampler.sIIR {
		snap.ResamplerIIR[i] = float32(resampler.sIIR[i])
	}
	for i := range resampler.sFIR {
		snap.ResamplerFIR[i] = float32(resampler.sFIR[i]) * (1.0 / 32768.0)
	}
	for i := range resampler.delayBuf {
		if i >= len(snap.ResamplerDelay) {
			break
		}
		snap.ResamplerDelay[i] = float32(resampler.delayBuf[i]) * (1.0 / 32768.0)
	}
	return snap
}

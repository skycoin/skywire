//go:build gopus_dred || gopus_osce

package silk

// DecoderStateSnapshot exposes per-channel SILK state for DRED/deep-PLC oracle
// comparisons. Q14 and IIR state retain their native integer bits.
type DecoderStateSnapshot struct {
	LagPrev        int32
	LastGainIndex  int
	LossCount      int
	PrevSignalType int
	SMid           [2]float32
	OutBuf         [maxFrameLength + 2*maxSubFrameLength]float32
	SLPCQ14        [maxLPCOrder]int32
	ExcQ14         [maxFrameLength]int32
	ResamplerIIR   [6]int32
	ResamplerFIR   [8]float32
	ResamplerDelay [96]float32
}

// SnapshotDecoderState returns a DecoderStateSnapshot for the given channel,
// preserving native int32 state and expressing int16 PCM history as normalized
// float32. It returns zero for a nil decoder or an out-of-range channel.
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
	snap.SLPCQ14 = st.sLPCQ14Buf
	snap.ExcQ14 = st.excQ14
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
			snap.ResamplerIIR[i] = down.sIIR[i]
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
	snap.ResamplerIIR = resampler.sIIR
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

//go:build gopus_dred || gopus_osce

package celt

func (d *Decoder) updatePostfilterHistoryMonoFromFloat32(samples []float32, frameSize int, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	d.materializePostfilterHistoryFromPLC()
	d.postfilterMemFromPLC = false
	d.postfilterMemPLCBacked = false
	updateMonoHistoryFromFloat32(d.postfilterMem[:history], samples, frameSize, history)
}

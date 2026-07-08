//go:build gopus_qext

package celt

type decoderQEXTFields struct {
	qext *decoderQEXTState
}

func (d *Decoder) qextState() *decoderQEXTState {
	return d.qext
}

func (d *Decoder) ensureQEXTState() *decoderQEXTState {
	if d.qext == nil {
		d.qext = &decoderQEXTState{}
	}
	return d.qext
}

func (d *Decoder) clearQEXTState() {
	if d.qext == nil {
		return
	}
	d.qext.pendingPayload = nil
	for i := range d.qext.oldBandE {
		d.qext.oldBandE[i] = 0
	}
}

func (d *Decoder) growQEXTOldBandE(needed int) {
	if d.qext == nil || len(d.qext.oldBandE) == 0 || len(d.qext.oldBandE) >= needed {
		return
	}
	prev := make([]celtGLog, needed)
	copy(prev, d.qext.oldBandE)
	d.qext.oldBandE = prev
}

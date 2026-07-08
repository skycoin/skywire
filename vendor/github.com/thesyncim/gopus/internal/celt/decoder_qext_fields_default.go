//go:build !gopus_qext

package celt

type decoderQEXTFields struct{}

func (d *Decoder) qextState() *decoderQEXTState {
	return nil
}

func (d *Decoder) ensureQEXTState() *decoderQEXTState {
	return nil
}

func (d *Decoder) clearQEXTState() {}

func (d *Decoder) growQEXTOldBandE(_ int) {}

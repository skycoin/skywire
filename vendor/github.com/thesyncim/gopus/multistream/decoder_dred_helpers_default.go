//go:build !gopus_dred && !gopus_osce

package multistream

type decoderDREDState struct{}

func (d *Decoder) dredState() *decoderDREDState {
	return nil
}

func (d *Decoder) resetDREDRuntimeState() {}

func (d *Decoder) dredSidecarActive() bool {
	return false
}

func (d *Decoder) dredPayloadScannerActive() bool {
	return false
}

func (d *Decoder) clearDREDPayloadState() {}

func (d *Decoder) invalidateDREDPayloadState() {}

func (d *Decoder) maybeCacheDREDPayload(_ int, _ []byte) {}

func (d *Decoder) beginDREDRawMonoGoodFrameCapture(_ int, _ *streamState, _ int, _ []byte) func() {
	return nil
}

func (d *Decoder) markDREDUpdated(_ int) {}

func (d *Decoder) markDREDConcealedAll() {}

func (d *Decoder) decodeDREDPLCStream(_ int, _ int) ([]float32, bool, error) {
	return nil, false, nil
}

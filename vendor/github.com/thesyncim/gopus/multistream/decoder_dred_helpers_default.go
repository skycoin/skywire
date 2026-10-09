//go:build !gopus_dred && !gopus_osce

package multistream

import "github.com/thesyncim/gopus/internal/dnnblob"

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

func (d *Decoder) clearRawSILKHistory() {}

func (d *Decoder) bindDREDNeuralModels(_ *dnnblob.Blob, _ dnnblob.DecoderModelState) {}

func (d *Decoder) beginDREDRawMonoFrameCapture(_ int, _ *streamState, _ int, _ []byte) bool {
	return false
}

func (d *Decoder) endDREDRawMonoFrameCapture(_ int, _ *streamState) {
}

func (d *Decoder) markDREDUpdated(_ int) {}

func (d *Decoder) markDREDConcealedAll() {}

func (d *Decoder) decodeDREDPLCStream(_ int, _ int) ([]float32, bool, error) {
	return nil, false, nil
}

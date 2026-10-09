package multistream

import "github.com/thesyncim/gopus/internal/dnnblob"

// SetDNNBlob retains a validated USE_WEIGHTS_FILE blob. It binds OSCE models
// to each child decoder and DRED models to the parent's per-stream recovery
// state. A nil blob clears the model bindings. The per-stream OSCE state
// corresponds to libopus's silk_OSCE_struct
// and silk_OSCE_BWE_struct in each silk_channel_state.
func (d *Decoder) SetDNNBlob(blob *dnnblob.Blob) {
	d.dnnBlob = blob
	var models dnnblob.DecoderModelState
	if blob != nil {
		models = blob.DecoderModels()
	}
	d.pitchDNNLoaded = models.PitchDNN
	d.plcModelLoaded = models.PLC
	d.farganModelLoaded = models.FARGAN
	if !models.PLC {
		d.clearRawSILKHistory()
	}
	d.bindDREDNeuralModels(blob, models)
	d.setOSCEModelState(models)
	// Fan the blob out to the child stream decoders so each stream binds its
	// own OSCE LACE/NoLACE + OSCE BWE runtime models. Test stubs that do not
	// implement the concrete `streamState` are skipped (they have no SILK
	// path so the postfilter would not apply anyway).
	for _, dec := range d.decoders {
		if s, ok := dec.(*streamState); ok {
			_ = s.bindOSCEModels(blob)
		}
	}
}

// DNNBlobLoaded reports whether a validated model blob is retained.
func (d *Decoder) DNNBlobLoaded() bool {
	return d.dnnBlob != nil
}

// PitchDNNLoaded reports whether the retained blob contains libopus's shared
// decoder pitch model family.
func (d *Decoder) PitchDNNLoaded() bool {
	return d.pitchDNNLoaded
}

// PLCModelLoaded reports whether the retained blob contains the PLC model family.
func (d *Decoder) PLCModelLoaded() bool {
	return d.plcModelLoaded
}

// FARGANModelLoaded reports whether the retained blob contains the FARGAN model family.
func (d *Decoder) FARGANModelLoaded() bool {
	return d.farganModelLoaded
}

package multistream

import "github.com/thesyncim/gopus/internal/dnnblob"

// SetDNNBlob retains a validated USE_WEIGHTS_FILE blob for future optional
// extension paths. A nil blob clears the retained main-decoder model state.
// The blob is also fanned out to every child stream decoder so each stream
// can bind its own OSCE LACE/NoLACE and OSCE BWE runtime models (libopus
// keeps an independent `silk_OSCE_struct` per `silk_channel_state` and one
// `silk_OSCE_BWE_struct` per `silk_channel_state`; the multistream decoder
// mirrors that by giving every per-stream `streamState` its own copy of the
// runtime state).
func (d *Decoder) SetDNNBlob(blob *dnnblob.Blob) {
	d.dnnBlob = blob
	var models dnnblob.DecoderModelState
	if blob != nil {
		models = blob.DecoderModels()
	}
	d.pitchDNNLoaded = models.PitchDNN
	d.plcModelLoaded = models.PLC
	d.farganModelLoaded = models.FARGAN
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

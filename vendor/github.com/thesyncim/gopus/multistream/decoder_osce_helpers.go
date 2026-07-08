//go:build gopus_osce

package multistream

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
)

// setOSCEEnabled stores the per-stream user-toggle bits mirroring libopus
// DecControl.osce_method != OSCE_METHOD_NONE / enable_osce_bwe. The fanout from
// the multistream Decoder calls this on every child streamState; the actual
// postfilter application is further gated on bound models / mode / bandwidth /
// frame size inside the apply helpers.
func (d *streamState) setOSCELACEEnabled(enabled bool) {
	if d == nil {
		return
	}
	d.osceLACEEnabled = enabled
}

func (d *streamState) setOSCEBWEEnabled(enabled bool) {
	if d == nil {
		return
	}
	d.osceBWEEnabled = enabled
}

// bindOSCEModels attaches (or detaches) the libopus OSCE LACE/NoLACE and
// OSCE BWE models on the child stream's runtime state. A nil blob (or blob
// missing the relevant manifests) clears any prior binding. The helper
// follows the same shape as `bindOSCEBWEModel` / `bindOSCELACEModel` in
// package gopus so the per-stream and single-stream decoders behave
// identically.
func (d *streamState) bindOSCEModels(blob *dnnblob.Blob) error {
	if d == nil {
		return nil
	}
	var models dnnblob.DecoderModelState
	if blob != nil {
		models = blob.DecoderModels()
	}

	// LACE / NoLACE binding.
	if blob == nil || !models.OSCE {
		if d.osceState != nil {
			for ch := range d.osceState.laceRuntime {
				_ = d.osceState.laceRuntime[ch].SetModel(nil)
				_ = d.osceState.noLACERuntime[ch].SetModel(nil)
			}
			d.osceState.laceModel = nil
		}
	} else {
		laceModel, err := osceLACE.Load(blob)
		if err != nil {
			if d.osceState != nil {
				for ch := range d.osceState.laceRuntime {
					_ = d.osceState.laceRuntime[ch].SetModel(nil)
					_ = d.osceState.noLACERuntime[ch].SetModel(nil)
				}
				d.osceState.laceModel = nil
			}
			return err
		}
		if d.osceState == nil {
			d.osceState = &streamOSCEState{}
		}
		d.osceState.laceModel = laceModel
		for ch := range d.osceState.laceRuntime {
			if err := d.osceState.laceRuntime[ch].SetModel(laceModel); err != nil {
				for j := range d.osceState.laceRuntime {
					_ = d.osceState.laceRuntime[j].SetModel(nil)
					_ = d.osceState.noLACERuntime[j].SetModel(nil)
				}
				d.osceState.laceModel = nil
				return err
			}
			if err := d.osceState.noLACERuntime[ch].SetModel(laceModel); err != nil {
				for j := range d.osceState.laceRuntime {
					_ = d.osceState.laceRuntime[j].SetModel(nil)
					_ = d.osceState.noLACERuntime[j].SetModel(nil)
				}
				d.osceState.laceModel = nil
				return err
			}
		}
		d.osceState.laceFeatureState[0].Reset()
		d.osceState.laceFeatureState[1].Reset()
	}

	// OSCE BWE binding.
	if blob == nil || !models.OSCEBWE {
		if d.osceState != nil {
			for ch := range d.osceState.bweRuntime {
				_ = d.osceState.bweRuntime[ch].SetModel(nil)
			}
			d.osceState.bweModel = nil
		}
	} else {
		bweModel, err := osceBWE.LoadModel(blob)
		if err != nil {
			if d.osceState != nil {
				for ch := range d.osceState.bweRuntime {
					_ = d.osceState.bweRuntime[ch].SetModel(nil)
				}
				d.osceState.bweModel = nil
			}
			return err
		}
		if d.osceState == nil {
			d.osceState = &streamOSCEState{}
		}
		d.osceState.bweModel = bweModel
		for ch := range d.osceState.bweRuntime {
			if err := d.osceState.bweRuntime[ch].SetModel(blob); err != nil {
				for j := range d.osceState.bweRuntime {
					_ = d.osceState.bweRuntime[j].SetModel(nil)
				}
				d.osceState.bweModel = nil
				return err
			}
		}
		d.osceState.bweFeatures[0].Reset()
		d.osceState.bweFeatures[1].Reset()
	}

	// When both bindings end up empty, drop the lazy state so a follow-up
	// SetDNNBlob(nil) leaves the streamState lean.
	if d.osceState != nil && d.osceState.laceModel == nil && d.osceState.bweModel == nil {
		d.osceState = nil
	}
	return nil
}

func (d *streamState) resetOSCEPostfilterState() {
	if d == nil || d.osceState == nil {
		return
	}
	d.resetOSCELACEState(d.channels == 2)
	for ch := range d.osceState.bweRuntime {
		d.osceState.bweRuntime[ch].Reset()
		d.osceState.bweFeatures[ch].Reset()
	}
	d.osceState.prevBWEActive = false
	d.osceState.prevExtendedMode = bweModeNone
	d.osceState.bweMonoPrevNativeLast = 0
}

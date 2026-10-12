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
	d.osceLACEOverrideSet = true
}

// osceLACEEnabledForComplexity mirrors libopus's per-decode OSCE method
// selection. An explicit SetOSCELACE call overrides automatic selection;
// otherwise complexity 6 and above enables the complexity-selected method.
func (d *streamState) osceLACEEnabledForComplexity() bool {
	if d == nil {
		return false
	}
	if d.osceLACEOverrideSet {
		return d.osceLACEEnabled
	}
	return d.complexity >= 6
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
				_ = d.osceState.laceRuntime[ch].SetModelPreservingState(nil)
				_ = d.osceState.noLACERuntime[ch].SetModelPreservingState(nil)
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
			if err := d.osceState.laceRuntime[ch].SetModelPreservingState(laceModel); err != nil {
				for j := range d.osceState.laceRuntime {
					_ = d.osceState.laceRuntime[j].SetModel(nil)
					_ = d.osceState.noLACERuntime[j].SetModel(nil)
				}
				d.osceState.laceModel = nil
				return err
			}
			if err := d.osceState.noLACERuntime[ch].SetModelPreservingState(laceModel); err != nil {
				for j := range d.osceState.laceRuntime {
					_ = d.osceState.laceRuntime[j].SetModel(nil)
					_ = d.osceState.noLACERuntime[j].SetModel(nil)
				}
				d.osceState.laceModel = nil
				return err
			}
		}
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
			if err := d.osceState.bweRuntime[ch].SetModelPreservingState(blob); err != nil {
				for j := range d.osceState.bweRuntime {
					_ = d.osceState.bweRuntime[j].SetModel(nil)
				}
				d.osceState.bweModel = nil
				return err
			}
		}
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

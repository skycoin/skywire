//go:build gopus_osce

package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
)

// bindOSCELACEModel attaches (or detaches) the extra-control libopus OSCE
// LACE/NoLACE model to the decoder's runtime state. The bound state is consumed
// by the post-SILK OSCE LACE / NoLACE forward pass, so callers can verify both
// upstream `lace_*` / `nolace_*` weight loading and runtime execution.
//
// supported reflects the blob's `SupportsOSCE()` answer (i.e. both LACE
// and NoLACE manifests present); when false the helper clears any prior
// binding.
//
// Both postfilter families ship together in the upstream weights blob and
// share an `OSCEModel` instance, so the loader binds both in one pass.
func (d *Decoder) bindOSCELACEModel(blob *dnnblob.Blob, supported bool) error {
	if d == nil {
		return nil
	}
	if blob == nil || !supported {
		if d.osceLACE != nil {
			for ch := range d.osceLACE.osceLACERuntime {
				_ = d.osceLACE.osceLACERuntime[ch].SetModel(nil)
				_ = d.osceLACE.osceNoLACERuntime[ch].SetModel(nil)
			}
			d.osceLACE.osceLACEModel = nil
			d.osceLACE = nil
		}
		return nil
	}
	model, err := osceLACE.Load(blob)
	if err != nil {
		// Keep d.osceModelsLoaded as the blob-level signal (still true) but
		// drop any prior runtime binding so callers see Loaded()==false.
		if d.osceLACE != nil {
			for ch := range d.osceLACE.osceLACERuntime {
				_ = d.osceLACE.osceLACERuntime[ch].SetModel(nil)
				_ = d.osceLACE.osceNoLACERuntime[ch].SetModel(nil)
			}
			d.osceLACE.osceLACEModel = nil
			d.osceLACE = nil
		}
		return err
	}
	if d.osceLACE == nil {
		d.osceLACE = &decoderOSCELACEState{}
	}
	d.osceLACE.osceLACEModel = model
	// Mirror the OSCE BWE binding: keep all per-channel runtime states in
	// sync with the loaded model so the forward pass can dispatch on a
	// per-channel slot without extra plumbing. libopus does the same with a
	// shared `OSCEModel` and one `LACEState`/`NoLACEState` per channel.
	for ch := range d.osceLACE.osceLACERuntime {
		if err := d.osceLACE.osceLACERuntime[ch].SetModel(model); err != nil {
			for j := range d.osceLACE.osceLACERuntime {
				_ = d.osceLACE.osceLACERuntime[j].SetModel(nil)
				_ = d.osceLACE.osceNoLACERuntime[j].SetModel(nil)
			}
			d.osceLACE.osceLACEModel = nil
			d.osceLACE = nil
			return err
		}
		if err := d.osceLACE.osceNoLACERuntime[ch].SetModel(model); err != nil {
			for j := range d.osceLACE.osceLACERuntime {
				_ = d.osceLACE.osceLACERuntime[j].SetModel(nil)
				_ = d.osceLACE.osceNoLACERuntime[j].SetModel(nil)
			}
			d.osceLACE.osceLACEModel = nil
			d.osceLACE = nil
			return err
		}
	}
	// Feature extractor state is independent of the model weights but its
	// signal-history / numbits-smooth / pitch-hangover buffers must start
	// from zero on (re)bind to match `osce_init` in libopus.
	d.osceLACE.osceLACEFeatures[0].Reset()
	d.osceLACE.osceLACEFeatures[1].Reset()
	return nil
}

// osceLACEModelLoadedRuntime reports whether the decoder currently has a
// bound OSCE LACE/NoLACE model that the runtime can use. The bool mirrors
// the OSCE BWE `osceBWEModelLoadedRuntime` accessor and is intended for
// test parity assertions.
func (d *Decoder) osceLACEModelLoadedRuntime() bool {
	if d == nil || d.osceLACE == nil {
		return false
	}
	return d.osceLACE.osceLACEModel != nil && d.osceLACE.osceLACEModel.Loaded()
}

// Compile-time sanity: keep the model alias visible so we don't drop the
// dependency when refactoring.
var _ = (*osceLACE.Model)(nil)

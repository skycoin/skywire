//go:build gopus_dred && !gopus_osce

package gopus

import "github.com/thesyncim/gopus/internal/dnnblob"

// bindOSCELACEModel is a no-op outside of the explicit
// `gopus_osce` build. The DRED-only build
// retains the `osceModelsLoaded` blob-presence flag but never instantiates
// the runtime state.
func (d *Decoder) bindOSCELACEModel(_ *dnnblob.Blob, _ bool) error {
	return nil
}

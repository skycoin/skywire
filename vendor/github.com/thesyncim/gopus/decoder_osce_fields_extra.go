//go:build gopus_osce

package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/silk"
)

type decoderOSCEFields struct {
	// The callback is bound once; per-packet context stays decoder-owned.
	osceLACEHook         silk.NativePostfilterHook
	osceLACEHookChannels int
	osceLACEHookStereo   bool
	osceLACEHookMode     osceLACEMode

	osceModelsLoaded    bool
	osceLACEModelLoaded bool
	osceBWEModelLoaded  bool
	osceBWEEnabled      bool
	osceBWE             *decoderOSCEBWEState
	osceLACEEnabled     bool
	osceLACEOverrideSet bool
	osceLACE            *decoderOSCELACEState
}

func (d *Decoder) setOSCEModelState(models dnnblob.DecoderModelState) {
	d.osceModelsLoaded = models.OSCE
	d.osceLACEModelLoaded = models.OSCE
	d.osceBWEModelLoaded = models.OSCEBWE
}

func (d *Decoder) osceBWEActive() bool {
	return d != nil && d.osceBWEEnabled
}

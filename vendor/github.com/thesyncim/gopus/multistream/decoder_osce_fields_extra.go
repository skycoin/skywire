//go:build gopus_osce

package multistream

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/silk"
)

type decoderOSCEFields struct {
	osceModelsLoaded    bool
	osceBWEModelLoaded  bool
	osceBWEEnabled      bool
	osceLACEEnabled     bool
	osceLACEOverrideSet bool
}

type streamOSCEFields struct {
	// The callback is bound once; per-packet context stays decoder-owned.
	osceLACEHook         silk.NativePostfilterHook
	osceLACEHookChannels int
	osceLACEHookStereo   bool
	osceLACEHookMode     streamOSCELACEMode

	osceLACEEnabled     bool
	osceLACEOverrideSet bool
	osceBWEEnabled      bool
	osceState           *streamOSCEState
}

func (d *Decoder) setOSCEModelState(models dnnblob.DecoderModelState) {
	d.osceModelsLoaded = models.OSCE
	d.osceBWEModelLoaded = models.OSCEBWE
}

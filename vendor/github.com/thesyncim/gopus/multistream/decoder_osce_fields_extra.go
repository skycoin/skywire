//go:build gopus_osce

package multistream

import "github.com/thesyncim/gopus/internal/dnnblob"

type decoderOSCEFields struct {
	osceModelsLoaded   bool
	osceBWEModelLoaded bool
	osceBWEEnabled     bool
	osceLACEEnabled    bool
}

type streamOSCEFields struct {
	osceLACEEnabled bool
	osceBWEEnabled  bool
	osceState       *streamOSCEState
}

func (d *Decoder) setOSCEModelState(models dnnblob.DecoderModelState) {
	d.osceModelsLoaded = models.OSCE
	d.osceBWEModelLoaded = models.OSCEBWE
}

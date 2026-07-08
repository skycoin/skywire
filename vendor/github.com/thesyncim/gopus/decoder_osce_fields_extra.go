//go:build gopus_osce

package gopus

import "github.com/thesyncim/gopus/internal/dnnblob"

type decoderOSCEFields struct {
	osceModelsLoaded    bool
	osceLACEModelLoaded bool
	osceBWEModelLoaded  bool
	osceBWEEnabled      bool
	osceBWE             *decoderOSCEBWEState
	osceLACEEnabled     bool
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

func (d *Decoder) osceLACEActive() bool {
	return d != nil && d.osceLACEEnabled
}

//go:build gopus_dred && !gopus_osce

package gopus

import "github.com/thesyncim/gopus/internal/dnnblob"

type decoderOSCEFields struct {
	osceModelsLoaded    bool
	osceLACEModelLoaded bool
	osceBWEModelLoaded  bool
}

func (d *Decoder) setOSCEModelState(models dnnblob.DecoderModelState) {
	d.osceModelsLoaded = models.OSCE
	d.osceLACEModelLoaded = models.OSCE
	d.osceBWEModelLoaded = models.OSCEBWE
}

func (d *Decoder) osceBWEActive() bool {
	return false
}

func (d *Decoder) osceLACEActive() bool {
	return false
}

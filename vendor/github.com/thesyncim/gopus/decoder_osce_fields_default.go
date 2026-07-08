//go:build !gopus_dred && !gopus_osce

package gopus

import "github.com/thesyncim/gopus/internal/dnnblob"

type decoderOSCEFields struct{}

func (d *Decoder) setOSCEModelState(_ dnnblob.DecoderModelState) {}

func (d *Decoder) osceBWEActive() bool {
	return false
}

func (d *Decoder) osceLACEActive() bool {
	return false
}

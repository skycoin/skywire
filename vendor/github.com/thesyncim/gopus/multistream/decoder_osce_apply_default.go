//go:build !gopus_osce

package multistream

import "github.com/thesyncim/gopus/internal/silk"

// applyOSCEPostSilk is a no-op outside of the explicit
// `gopus_osce` build. The fanout call site in
// `streamState.decodeSILK` always invokes it so the shared code compiles on
// both builds; under the default tag it collapses to nothing.
func (d *streamState) applyOSCEPostSilk(_ []float32, _ int, _ silk.Bandwidth, _ bool) {
}

func (d *streamState) applyOSCEPLCSilk(_ []float32, _ int, _ silk.Bandwidth, _ bool) {
}

func (d *streamState) installOSCELACESilkPostfilterHook(_ silk.Bandwidth, _ bool) func() {
	return func() {}
}

func (d *streamState) markOSCEInactiveIfModeIneligible(_ streamTOC, _ []float32, _ int) {
}

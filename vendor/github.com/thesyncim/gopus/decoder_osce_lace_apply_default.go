//go:build !gopus_osce

package gopus

import "github.com/thesyncim/gopus/internal/silk"

func (d *Decoder) installOSCELACESilkPostfilterHook(_ Mode, _ silk.Bandwidth, _ bool) func() {
	return func() {}
}

// osceLACEMarkInactiveIfModeIneligible is a no-op stub on the default
// build. The `gopus_osce` build provides the
// real implementation in `decoder_osce_lace_apply.go`.
func (d *Decoder) osceLACEMarkInactiveIfModeIneligible(_ Mode, _ Bandwidth) {}

// resetOSCELACEPostfilterState is a no-op on the default build.
func (d *Decoder) resetOSCELACEPostfilterState(_ bool) {}

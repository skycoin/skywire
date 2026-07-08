//go:build !gopus_osce

package gopus

import "github.com/thesyncim/gopus/internal/silk"

// maybeApplyOSCEBWEPostSilk is a no-op outside of the explicit
// `gopus_osce` build. Default builds keep the OSCE BWE
// runtime inactive so the standard silk_resampler output is always used.
func (d *Decoder) maybeApplyOSCEBWEPostSilk(
	_ []float32,
	_ int,
	_ Mode,
	_ silk.Bandwidth,
	_ bool,
) bool {
	return false
}

// osceBWEMarkInactiveIfModeIneligible is a no-op stub on the default build.
func (d *Decoder) osceBWEMarkInactiveIfModeIneligible(_ Mode, _ Bandwidth, _ []float32, _ int, _ bool) {
}

// resetOSCEBWEPostfilterState is a no-op on the default build.
func (d *Decoder) resetOSCEBWEPostfilterState() {}

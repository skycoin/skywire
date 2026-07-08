//go:build gopus_dred || gopus_osce

package hybrid

// SyncCELTAfterDREDLoss aligns the retained inner CELT cadence with a
// DRED/neural loss so the next hybrid packet follows the same loss-history
// branch libopus would use.
func (d *Decoder) SyncCELTAfterDREDLoss() {
	if d == nil || d.celtDecoder == nil {
		return
	}
	d.celtDecoder.SyncAfterDREDLoss()
}

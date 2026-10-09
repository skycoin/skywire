//go:build gopus_osce || gopus_dred

package multistream

// DREDModelLoaded reports whether the retained blob contains the DRED decoder
// model family. This method is available in builds tagged gopus_dred or
// gopus_osce.
func (d *Decoder) DREDModelLoaded() bool {
	return d != nil && d.dred != nil && d.dred.dredModelLoaded
}

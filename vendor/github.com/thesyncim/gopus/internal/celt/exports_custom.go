//go:build gopus_custom_modes

package celt

// NcwrsUrowExport exposes ncwrsUrow for the custom-mode pulse-cache builder.
// It computes V(n,k) and fills u with U(n,0..k+1); u must have length >= k+2.
//
// Reference: libopus celt/cwrs.c ncwrs_urow().
func NcwrsUrowExport(n, k int, u []uint32) uint32 {
	return ncwrsUrow(n, k, u)
}

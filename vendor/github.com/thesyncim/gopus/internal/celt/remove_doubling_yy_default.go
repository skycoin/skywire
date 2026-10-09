//go:build !amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package celt

// Keep the source expression for targets outside the pinned x86-v3 float core.
func removeDoublingYYUpdate32(yy, xBefore, xAfter float32) float32 {
	yy += xBefore * xBefore
	yy -= xAfter * xAfter
	return yy
}

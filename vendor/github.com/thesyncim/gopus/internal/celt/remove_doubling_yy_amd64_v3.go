//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

// removeDoublingYYUpdate32 follows the float update in celt/pitch.c:
// yy = yy + x[-i]*x[-i] - x[N-i]*x[N-i]. The selected scalar and SSE
// x86-v3 remove_doubling callers round both products and the intervening add
// separately.
func removeDoublingYYUpdate32(yy, xBefore, xAfter float32) float32 {
	productBefore := round32(xBefore * xBefore)
	updated := round32(yy + productBefore)
	productAfter := round32(xAfter * xAfter)
	return round32(updated - productAfter)
}

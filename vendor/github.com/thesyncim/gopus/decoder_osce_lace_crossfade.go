//go:build gopus_osce

package gopus

// osceLACECrossFade10ms mirrors libopus dnn/osce_features.c
// `osce_cross_fade_10ms`. It blends the first 10 ms of an enhanced 16 kHz
// LACE/NoLACE output buffer (`xEnhanced`) against the raw, pre-enhancement
// input (`xIn`) using the libopus `osce_window[]` half-window weights:
//
//	x_enhanced[i] = w[i] * x_enhanced[i] + (1 - w[i]) * x_in[i]    for i in [0, 160)
//
// The libopus implementation operates on float32 buffers; the helper writes
// the cross-fade back into `xEnhanced` so callers can keep using it as the
// effective LACE/NoLACE output. The buffer length must be at least 160
// samples (10 ms @ 16 kHz). The trailing 160 samples are left untouched so
// callers can continue to consume the enhanced LACE/NoLACE output for the
// second half of the 20 ms frame.
//
// libopus invokes this helper on the frame immediately after a non-LACE ->
// LACE/NoLACE mode transition (`psDec->osce.features.reset == 1`). The
// gopus wiring drives the same fade on the first SILK postfilter-active
// frame after a non-postfilter frame (mirroring `prevLACEActive` flipping
// from false to true).
//
// Re-uses the `osceWindow` constant table from
// `decoder_osce_bwe_crossfade.go` so the LACE / BWE cross-fades share the
// same 320-entry weight table that ships with libopus.
func osceLACECrossFade10ms(xEnhanced, xIn []float32, length int) {
	if length < 160 {
		return
	}
	if len(xEnhanced) < 160 || len(xIn) < 160 {
		return
	}
	for i := 0; i < 160; i++ {
		w := osceWindow[i]
		xEnhanced[i] = w*xEnhanced[i] + (1.0-w)*xIn[i]
	}
}

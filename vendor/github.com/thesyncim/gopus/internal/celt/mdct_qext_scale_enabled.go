//go:build gopus_qext

package celt

// mdctQEXTScalePlacement selects the ENABLE_QEXT forward-MDCT scale placement.
//
// In a QEXT libopus build, clt_mdct_forward() does NOT fold the 1/nfft FFT scale
// into the pre-rotation (yc.r = yr; yc.i = yi), and instead multiplies the
// post-rotation twiddles by the scale (t0 = S_MUL2(t[i], scale)). The default
// (non-QEXT) build does the opposite: scale in the pre-rotation, raw twiddles in
// the post-rotation. The two placements round differently (tens of ULP on a
// short transform), so the gopus_qext build must mirror the QEXT placement to
// stay byte-exact with the QEXT oracle.
const mdctQEXTScalePlacement = true

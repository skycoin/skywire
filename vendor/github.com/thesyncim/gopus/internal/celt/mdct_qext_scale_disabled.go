//go:build !gopus_qext

package celt

// mdctQEXTScalePlacement is false in the default (non-QEXT) build: the forward
// MDCT folds the 1/nfft FFT scale into the pre-rotation and uses raw twiddles in
// the post-rotation, matching the non-QEXT clt_mdct_forward().
const mdctQEXTScalePlacement = false

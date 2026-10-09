//go:build arm64

package celt

// Match the pinned libopus arm64 float-path accumulation pattern on long-frame
// CELT MDCT mixes. This closes real packet-decision drift on parity fixtures.
const mdctUseFMALikeMixEnabled = true
const mdctUseFusedForwardPreRotate = true
const mdctUseNegFoldSecondProduct = false

// imdctPreRotate mirrors the clang -ffp-contract=on float path of libopus
// celt/mdct.c clt_mdct_backward_c() pre-rotation
// (yr=S_MUL(x2,t[i])+S_MUL(x1,t[N4+i]), yi=S_MUL(x1,t[i])-S_MUL(x2,t[N4+i])):
// each output rounds its second product on its own and fuses the first
// multiply into the add/sub.
func imdctPreRotate(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	imdctPreRotateFMA32Kiss(fftIn, spectrum, trig, n2, n4)
}

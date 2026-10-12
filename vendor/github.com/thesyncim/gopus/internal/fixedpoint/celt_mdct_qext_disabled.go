//go:build gopus_fixed_point && !gopus_qext

package fixedpoint

type qextMDCTScratch struct{}

func (e *CELTEncoder) mdctForward(in, out []int32, overlap, shift, stride int, scratch *celtEncodeScratch) {
	e.mdct.MDCTForward(in, out, e.window, overlap, shift, stride, scratch)
}

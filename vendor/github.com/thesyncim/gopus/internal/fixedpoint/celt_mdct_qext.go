//go:build gopus_fixed_point && gopus_qext

package fixedpoint

type qextMDCTScratch struct {
	mdct QEXTMDCTScratch
}

// mdctForward uses the Q31 CELT transform selected by ENABLE_QEXT, including
// frames for which the encoder reserves no extension payload.
func (e *CELTEncoder) mdctForward(in, out []int32, overlap, shift, stride int, scratch *celtEncodeScratch) {
	lookup := e.qext.customMDCT
	if lookup == nil {
		lookup = NewStaticQEXTMDCTLookup48000()
		if e.modeFs == 96000 {
			lookup = NewStaticQEXTMDCTLookup96000()
		}
	}
	lookup.MDCTForward(in, out, nil, overlap, shift, stride, &scratch.qextMDCT.mdct)
}

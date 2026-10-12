//go:build gopus_fixed_point && !gopus_qext

package fixedpoint

type surroundAnalysisMDCT struct {
	lookup  *MDCTLookup
	scratch celtEncodeScratch
}

func newSurroundAnalysisMDCT() surroundAnalysisMDCT {
	return surroundAnalysisMDCT{lookup: NewStaticMDCTLookup48000()}
}

func (m *surroundAnalysisMDCT) forward(in, out []int32, lm int) {
	m.lookup.MDCTForward(in, out, staticMDCT48000Window[:], celtOverlap,
		staticMDCT48000MaxShift-lm, 1, &m.scratch)
}

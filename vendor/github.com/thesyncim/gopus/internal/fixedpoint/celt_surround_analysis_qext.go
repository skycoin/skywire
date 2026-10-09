//go:build gopus_fixed_point && gopus_qext

package fixedpoint

type surroundAnalysisMDCT struct {
	lookup  *QEXTMDCTLookup
	scratch QEXTMDCTScratch
}

func newSurroundAnalysisMDCT() surroundAnalysisMDCT {
	return surroundAnalysisMDCT{lookup: NewStaticQEXTMDCTLookup48000()}
}

func (m *surroundAnalysisMDCT) forward(in, out []int32, lm int) {
	m.lookup.MDCTForward(in, out, nil, celtOverlap,
		staticMDCT48000MaxShift-lm, 1, &m.scratch)
}

//go:build gopus_fixed_point

package fixedpoint

// SurroundAnalysis owns the scratch used by the FIXED_POINT multistream
// surround analyzer. It computes one CELT band's Q24 log energies at a time.
type SurroundAnalysis struct {
	mdct  surroundAnalysisMDCT
	freq  []int32
	bandE [21]int32
}

// NewSurroundAnalysis creates a reusable analyzer for the 48 kHz CELT mode.
func NewSurroundAnalysis() *SurroundAnalysis {
	return &SurroundAnalysis{mdct: newSurroundAnalysisMDCT()}
}

// BandLogEInto applies one integer MDCT and computes the 21 band log energies
// for a surround-analysis frame. in contains shortMdctSize<<lm signal samples
// followed by the CELT overlap. The output uses Q24 celt_glog values.
func (a *SurroundAnalysis) BandLogEInto(in []int32, lm, upsample int, out []int32) bool {
	if a == nil {
		return false
	}
	if !a.BandEnergyInto(in, lm, upsample, a.bandE[:]) {
		return false
	}
	Amp2Log2(a.bandE[:], out, celtNbEBands, celtNbEBands, celtNbEBands, 1)
	return true
}

// BandEnergyInto applies one integer MDCT and computes its 21 CELT band
// amplitudes. The energies are returned before amp2Log2, which lets the
// multistream analyzer take the source-matched maximum across subframes first.
func (a *SurroundAnalysis) BandEnergyInto(in []int32, lm, upsample int, out []int32) bool {
	if a == nil || lm < 0 || lm > staticMDCT48000MaxShift || upsample < 1 || len(out) < len(a.bandE) {
		return false
	}
	n := celtShortMdctSize << lm
	if len(in) < n+celtOverlap {
		return false
	}
	freq := ensureInt32(&a.freq, n)
	a.mdct.forward(in, freq, lm)
	if upsample > 1 {
		bound := n / upsample
		for i := 0; i < bound; i++ {
			freq[i] *= int32(upsample)
		}
		clear(freq[bound:])
	}
	ComputeBandEnergies(freq, staticMDCT48000EBands[:], staticMDCT48000LogN[:], a.bandE[:],
		celtNbEBands, celtShortMdctSize, celtNbEBands, 1, lm)
	copy(out, a.bandE[:])
	return true
}

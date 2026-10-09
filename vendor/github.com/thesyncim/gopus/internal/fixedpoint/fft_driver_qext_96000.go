//go:build gopus_fixed_point && gopus_qext

package fixedpoint

type staticQEXTKFFTDef struct {
	nfft       int
	scaleShift int
	shift      int
	factors    [2 * maxFactors]int16
	bitrev     []int16
}

var staticQEXTMDCT96000KFFT = [...]staticQEXTKFFTDef{
	{nfft: 960, scaleShift: 9, shift: -1, factors: [2 * maxFactors]int16{5, 192, 3, 64, 4, 16, 4, 4, 4, 1}, bitrev: staticQEXTMDCT96000Bitrev[:]},
	{nfft: 480, scaleShift: 8, shift: 1, factors: [2 * maxFactors]int16{5, 96, 3, 32, 4, 8, 2, 4, 4, 1}, bitrev: staticMDCT48000Bitrev0[:]},
	{nfft: 240, scaleShift: 7, shift: 2, factors: [2 * maxFactors]int16{5, 48, 3, 16, 4, 4, 4, 1}, bitrev: staticMDCT48000Bitrev1[:]},
	{nfft: 120, scaleShift: 6, shift: 3, factors: [2 * maxFactors]int16{5, 24, 3, 8, 2, 4, 4, 1}, bitrev: staticMDCT48000Bitrev2[:]},
}

var staticQEXTMDCTLookup96000 = buildStaticQEXTMDCTLookup96000()

func buildStaticQEXTMDCTLookup96000() *QEXTMDCTLookup {
	l := &QEXTMDCTLookup{
		n:        3840,
		maxshift: 3,
		kfft:     make([]*QEXTKissFFTState, len(staticQEXTMDCT96000KFFT)),
		trig:     staticQEXTMDCT96000Trig[:],
		window:   staticQEXTMDCT96000Window[:],
	}
	for i, base := range staticQEXTMDCT96000KFFT {
		l.kfft[i] = &QEXTKissFFTState{
			nfft:       base.nfft,
			scale:      572662306,
			scaleShift: base.scaleShift,
			shift:      base.shift,
			factors:    base.factors,
			bitrev:     base.bitrev,
			twiddles:   staticQEXTMDCT96000Twiddles[:],
		}
	}
	return l
}

// NewStaticQEXTMDCTLookup96000 returns the shared static 96000/1920 mode lookup.
func NewStaticQEXTMDCTLookup96000() *QEXTMDCTLookup {
	return staticQEXTMDCTLookup96000
}

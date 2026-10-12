//go:build gopus_fixed_point

package fixedpoint

// StaticCELT48000FFTState returns the baked 480-point KISS-FFT state used by
// the 48 kHz CELT mode's MDCT lookup. src/analysis.c uses
// celt_mode->mdct.kfft[0] for its fixed-point tonality FFT, so reusing this
// table keeps analysis twiddles and bit-reversal identical to libopus.
func StaticCELT48000FFTState() *KissFFTState {
	def := staticMDCT48000KFFT[0]
	return &KissFFTState{
		nfft:       def.nfft,
		scale:      def.scale,
		scaleShift: def.scaleShift,
		shift:      def.shift,
		factors:    def.factors,
		bitrev:     def.bitrev,
		twiddles:   staticMDCT48000Twiddles[:],
	}
}

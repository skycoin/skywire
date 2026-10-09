//go:build gopus_fixed_point && gopus_qext && gopus_custom_modes

package fixedpoint

import "math"

// NewQEXTMDCTLookup builds the Q31 transform and overlap window for a custom
// mode. Initialization follows clt_mdct_init(), opus_fft_alloc_twiddles() and
// opus_custom_mode_create() in libopus. Those helpers use C double for table
// generation; the transform itself stores and operates on int32 coefficients.
func NewQEXTMDCTLookup(n, maxshift, overlap int) *QEXTMDCTLookup {
	if maxshift < 0 || maxshift > 3 || n < 8<<maxshift || n > 4096 ||
		n%(4<<maxshift) != 0 || overlap <= 0 || overlap%4 != 0 || overlap > n>>(maxshift+1) {
		return nil
	}
	l := &QEXTMDCTLookup{
		n: n, maxshift: maxshift,
		kfft:   make([]*QEXTKissFFTState, maxshift+1),
		trig:   make([]int32, n-(n>>(maxshift+1))),
		window: make([]int32, overlap),
	}
	for shift := 0; shift <= maxshift; shift++ {
		st := &QEXTKissFFTState{nfft: n >> (shift + 2), shift: -1}
		if !kfFactor(st.nfft, &st.factors) {
			return nil
		}
		st.scaleShift = int(CeltILog2(int32(st.nfft)))
		st.scale = int32(((int64(1)<<30)<<st.scaleShift + int64(st.nfft/2)) / int64(st.nfft))
		st.bitrev = make([]int16, st.nfft)
		computeBitrevTable(0, st.bitrev, 0, 1, 1, st.factors[:], 0)
		if shift == 0 {
			st.twiddles = make([]QEXTFFTTwiddle, st.nfft)
			step := 2 * math.Pi / float64(st.nfft)
			for i := range st.twiddles {
				phase := step * -float64(i)
				st.twiddles[i] = QEXTFFTTwiddle{
					R: int32(math.Min(2147483647, math.Floor(.5+2147483648*math.Cos(phase)))),
					I: int32(math.Min(2147483647, math.Floor(.5+2147483648*math.Sin(phase)))),
				}
			}
		} else {
			st.shift = shift
			st.twiddles = l.kfft[0].twiddles
		}
		l.kfft[shift] = st
	}
	for shift, offset := 0, 0; shift <= maxshift; shift++ {
		length := n >> shift
		for i := range length / 2 {
			phase := 2 * math.Pi * (float64(i) + .125) / float64(length)
			l.trig[offset+i] = int32(math.Max(-2147483647,
				math.Min(2147483647, math.Floor(.5+2147483648*math.Cos(phase)))))
		}
		offset += length / 2
	}
	for i := range l.window {
		phase := .5 * math.Pi * (float64(i) + .5) / float64(overlap)
		s := math.Sin(phase)
		// modes.c truncates Q31 windows; Q15 windows instead round with floor.
		l.window[i] = int32(math.Min(2147483647, 2147483648*math.Sin(.5*math.Pi*s*s)))
	}
	return l
}

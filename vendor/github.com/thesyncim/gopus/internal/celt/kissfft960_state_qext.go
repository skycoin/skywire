//go:build gopus_qext

package celt

// The native 96 kHz mode in libopus uses the static 960-point FFT twiddles in
// celt/static_modes_float.h. Its bit-reversal and radix factors match the
// ordinary 960-point KISS state.
var kissFFTState960QEXT = func() *kissFFTState {
	state := newDynamicKissFFTState(960, nil)
	state.w = fftTwiddles960QEXTStatic[:]
	state.stageTw = newKissStageTwiddles(state.factors, state.fstride, state.shift, state.w)
	return state
}()

func getKissFFTState960() *kissFFTState { return kissFFTState960QEXT }

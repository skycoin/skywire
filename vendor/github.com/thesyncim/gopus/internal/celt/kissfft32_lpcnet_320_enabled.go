//go:build gopus_dred || gopus_osce

package celt

var kissFFTState320 = newKissFFTState(320)

func getKissFFTState320() *kissFFTState {
	return kissFFTState320
}

func staticKissFFT320Bitrev() []int {
	return fftBitrevLPCNet320Static[:]
}

func staticKissFFT320Twiddles() []kissCpx {
	return fftTwiddlesLPCNet320Static[:]
}

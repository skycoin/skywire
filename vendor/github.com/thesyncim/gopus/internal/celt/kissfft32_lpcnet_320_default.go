//go:build !gopus_dred && !gopus_osce

package celt

import "sync"

var (
	kissFFTState320Once    sync.Once
	kissFFTState320Default *kissFFTState
)

func getKissFFTState320() *kissFFTState {
	kissFFTState320Once.Do(func() {
		kissFFTState320Default = newKissFFTState(320)
	})
	return kissFFTState320Default
}

func staticKissFFT320Bitrev() []int {
	return nil
}

func staticKissFFT320Twiddles() []kissCpx {
	return nil
}

//go:build !gopus_qext

package celt

func getKissFFTState960() *kissFFTState { return newKissFFTState(960) }

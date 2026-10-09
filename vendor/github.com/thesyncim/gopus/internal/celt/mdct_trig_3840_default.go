//go:build !gopus_qext

package celt

func getMDCTTrig3840F32() []float32 { return buildMDCTTrigF32(3840) }

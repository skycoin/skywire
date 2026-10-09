//go:build !gopus_celt_trace || gopus_fixed_point

package celt

const removeDoublingMathTraceCaptureEnabled = false

func recordRemoveDoublingDual(float32, float32) {}

func recordRemoveDoublingYY(int, float32, float32, float32, float32) {}

func recordRemoveDoublingGain(float32, float32, float32, float32, float32, float32) {}

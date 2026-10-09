package celt

// PitchXCorrFloat32 runs the selected CELT pitch correlation kernel used by
// libopus dnn/nndsp.c:adaconv_process_frame and dnn/lpcnet_enc.c:compute_frame_features.
// The caller supplies len(x) >= length, len(y) >= length+maxPitch-1, and
// len(dst) >= maxPitch.
func PitchXCorrFloat32(dst, x, y []float32, length, maxPitch int) {
	pitchXCorrFloat32(x, y, dst, length, maxPitch)
}

// LPCNetInnerProdFloat32 runs the selected celt_inner_prod kernel at the same
// libopus compute_frame_features callsite.
func LPCNetInnerProdFloat32(x, y []float32, length int) float32 {
	return innerProdFloat32(x, y, length)
}

// LPCNetFIRXCorrKernel4Float32 runs the selected CELT xcorr kernel used by
// libopus celt_fir_c from dnn/lpcnet_enc.c. On amd64 SIMD builds this keeps
// the SSE even/odd accumulator order selected by celt/x86/x86_celt_map.c.
func LPCNetFIRXCorrKernel4Float32(x, y []float32, sum *[4]float32, length int) {
	celtLPCXcorrKernel4Float32(x, y, sum, length)
}

package silk

func celtPitchXcorrFloatImplScalar(x, y []float32, out []float32, length, maxPitch int) {
	_ = out[maxPitch-1] // BCE hint
	i := 0
	for ; i < maxPitch-3; i += 4 {
		if len(y)-i < length+3 {
			break
		}
		var sum [4]float32
		xcorrKernelFloat(x, y[i:], &sum, length)
		out[i] = sum[0]
		out[i+1] = sum[1]
		out[i+2] = sum[2]
		out[i+3] = sum[3]
	}
	for ; i < maxPitch; i++ {
		out[i] = innerProductF32Acc(x, y[i:], length)
	}
}

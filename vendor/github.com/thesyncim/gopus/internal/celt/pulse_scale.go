package celt

func scalePulsesIntoScalar(out []celtNorm, pulses []int32, g float32) {
	n := len(pulses)
	out = out[:n]
	i := 0
	for ; i+4 <= n; i += 4 {
		p := pulses[i : i+4 : i+4]
		o := out[i : i+4 : i+4]
		o[0] = celtNorm(float32(p[0]) * g)
		o[1] = celtNorm(float32(p[1]) * g)
		o[2] = celtNorm(float32(p[2]) * g)
		o[3] = celtNorm(float32(p[3]) * g)
	}
	for ; i < n; i++ {
		out[i] = celtNorm(float32(pulses[i]) * g)
	}
}

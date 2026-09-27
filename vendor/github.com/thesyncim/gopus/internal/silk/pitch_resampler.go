package silk

import "github.com/thesyncim/gopus/internal/opusmath"

const (
	resamplerDown2_0 = 9872
	resamplerDown2_1 = -25727
)

var resampler23CoefsLQ = [6]int16{
	-2797, -6507,
	4697, 10739,
	1567, 8276,
}

func floatToInt16Round(x float32) int16 {
	return opusmath.Float32ToInt16Raw(x)
}

func floatToInt16SliceScaled(out []int16, in []float32, scale float32) {
	n := min(len(out), len(in))
	if n <= 0 {
		return
	}
	_ = in[n-1]  // BCE hint
	_ = out[n-1] // BCE hint
	floatToInt16Scaled(out, in, scale, n)
}

func int16ToFloat32Slice(out []float32, in []int16) {
	n := min(len(out), len(in))
	if n <= 0 {
		return
	}
	_ = in[n-1]  // BCE hint
	_ = out[n-1] // BCE hint
	for i := range n {
		out[i] = float32(in[i])
	}
}

func resamplerDown2(state *[2]int32, out []int16, in []int16) int {
	inLen := len(in)
	outLen := min(inLen/2, len(out))
	if outLen <= 0 {
		return 0
	}
	_ = in[2*outLen-1] // BCE hint: prove all in[2*k] and in[2*k+1] accesses are valid
	_ = out[outLen-1]  // BCE hint
	for k := range outLen {
		in32 := int32(in[2*k]) << 10
		y := in32 - state[0]
		x := silkSMLAWB(y, y, resamplerDown2_1)
		out32 := state[0] + x
		state[0] = in32 + x

		in32 = int32(in[2*k+1]) << 10
		y = in32 - state[1]
		x = silkSMULWB(y, resamplerDown2_0)
		out32 += state[1]
		out32 += x
		state[1] = in32 + x

		out[k] = silkSAT16(silkRSHIFT_ROUND(out32, 11))
	}
	return outLen
}

func resamplerPrivateAR2(state *[2]int32, out []int32, in []int16, a0, a1 int16) {
	n := min(len(out), len(in))
	for k := range n {
		out32 := silkADD_LSHIFT32(state[0], int32(in[k]), 8)
		out[k] = out32
		out32 = out32 << 2
		state[0] = silkSMLAWB(state[1], out32, int32(a0))
		state[1] = silkSMULWB(out32, int32(a1))
	}
}

func resamplerDown2_3(state *[6]int32, out []int16, in []int16, scratch []int32) int {
	const orderFIR = 4
	inLen := len(in)
	if inLen == 0 {
		return 0
	}
	bufLen := inLen + orderFIR
	if len(scratch) < bufLen {
		scratch = make([]int32, bufLen)
	}
	buf := scratch[:bufLen]
	for i := range orderFIR {
		buf[i] = state[i]
	}

	arState := [2]int32{state[orderFIR], state[orderFIR+1]}
	resamplerPrivateAR2(&arState, buf[orderFIR:], in, resampler23CoefsLQ[0], resampler23CoefsLQ[1])
	state[orderFIR] = arState[0]
	state[orderFIR+1] = arState[1]

	outIdx := 0
	bufPtr := 0
	counter := inLen
	for counter > 2 && outIdx+1 < len(out) {
		resQ6 := silkSMULWB(buf[bufPtr+0], int32(resampler23CoefsLQ[2]))
		resQ6 = silkSMLAWB(resQ6, buf[bufPtr+1], int32(resampler23CoefsLQ[3]))
		resQ6 = silkSMLAWB(resQ6, buf[bufPtr+2], int32(resampler23CoefsLQ[5]))
		resQ6 = silkSMLAWB(resQ6, buf[bufPtr+3], int32(resampler23CoefsLQ[4]))
		out[outIdx] = silkSAT16(silkRSHIFT_ROUND(resQ6, 6))
		outIdx++

		resQ6 = silkSMULWB(buf[bufPtr+1], int32(resampler23CoefsLQ[4]))
		resQ6 = silkSMLAWB(resQ6, buf[bufPtr+2], int32(resampler23CoefsLQ[5]))
		resQ6 = silkSMLAWB(resQ6, buf[bufPtr+3], int32(resampler23CoefsLQ[3]))
		resQ6 = silkSMLAWB(resQ6, buf[bufPtr+4], int32(resampler23CoefsLQ[2]))
		out[outIdx] = silkSAT16(silkRSHIFT_ROUND(resQ6, 6))
		outIdx++

		bufPtr += 3
		counter -= 3
	}

	copy(state[:orderFIR], buf[inLen:inLen+orderFIR])
	return outIdx
}

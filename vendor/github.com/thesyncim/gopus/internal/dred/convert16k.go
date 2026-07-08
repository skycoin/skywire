package dred

import "github.com/thesyncim/gopus/internal/opusmath"

const (
	// ResamplingOrder mirrors libopus RESAMPLING_ORDER in dred_encoder.h.
	ResamplingOrder = 8
	// MaxConvert16kBuffer mirrors libopus MAX_DOWNMIX_BUFFER without QEXT.
	MaxConvert16kBuffer = 1920
)

const dredVerySmall float32 = 1e-30

type dred16kFilterSpec struct {
	b0 float32
	b  [ResamplingOrder]float32
	a  [ResamplingOrder]float32
}

var (
	dred48k24kTo16kFilter = dred16kFilterSpec{
		b0: 0.004523418224,
		b: [ResamplingOrder]float32{
			0.005873358047, 0.012980854831, 0.014531340042, 0.014531340042,
			0.012980854831, 0.005873358047, 0.004523418224, 0,
		},
		a: [ResamplingOrder]float32{
			-3.878718597768, 7.748834257468, -9.653651699533, 8.007342726666,
			-4.379450178552, 1.463182111810, -0.231720677804, 0,
		},
	}
	dred12kTo16kFilter = dred16kFilterSpec{
		b0: 0.002033596776,
		b: [ResamplingOrder]float32{
			-0.001017101081, 0.003673127243, 0.001009165267, 0.001009165267,
			0.003673127243, -0.001017101081, 0.002033596776, 0,
		},
		a: [ResamplingOrder]float32{
			-4.930414411612, 11.291643096504, -15.322037343815, 13.216403930898,
			-7.220409219553, 2.310550142771, -0.334338618782, 0,
		},
	}
	dred8kTo16kFilter = dred16kFilterSpec{
		b0: 0.020109185709,
		b: [ResamplingOrder]float32{
			0.081670120929, 0.180401598565, 0.259391051971, 0.259391051971,
			0.180401598565, 0.081670120929, 0.020109185709, 0,
		},
		a: [ResamplingOrder]float32{
			-1.393651933659, 2.609789872676, -2.403541968806, 2.056814957331,
			-1.148908574570, 0.473001413788, -0.110359852412, 0,
		},
	}
)

// ConvertTo16kMonoFloat32 mirrors libopus dred_convert_to_16k() for callers
// that already hold float32 PCM. It keeps the downmix and quantization loop in
// float32, matching libopus' float input path without widening roundtrips.
func ConvertTo16kMonoFloat32(dst []float32, mem *[ResamplingOrder + 1]float32, in []float32, sampleRate, channels int) int {
	if channels != 1 && channels != 2 {
		return 0
	}
	if len(in) == 0 || len(in)%channels != 0 {
		return 0
	}
	inLen := len(in) / channels
	outLen := inLen * 16000 / sampleRate
	if outLen <= 0 || outLen > len(dst) {
		return 0
	}

	up, ok := dred16kUpsampleFactor(sampleRate)
	if !ok {
		return 0
	}
	workLen := up * inLen
	if workLen > MaxConvert16kBuffer {
		return 0
	}

	switch sampleRate {
	case 16000:
		for i := range inLen {
			dst[i] = dred16kDownmixSampleFloat32(in, i, channels, up)
		}
		return outLen
	case 48000, 24000:
		return dredFilterZeroStuffedTo16kFloat32(dst[:outLen], in, channels, up, workLen, 3, dred48k24kTo16kFilter, mem)
	case 12000:
		return dredFilterZeroStuffedTo16kFloat32(dst[:outLen], in, channels, up, workLen, 3, dred12kTo16kFilter, mem)
	case 8000:
		return dredFilterZeroStuffedTo16kFloat32(dst[:outLen], in, channels, up, workLen, 1, dred8kTo16kFilter, mem)
	default:
		return 0
	}
}

func dred16kUpsampleFactor(sampleRate int) (int, bool) {
	switch sampleRate {
	case 8000:
		return 2, true
	case 12000:
		return 4, true
	case 16000:
		return 1, true
	case 24000:
		return 2, true
	case 48000:
		return 1, true
	default:
		return 0, false
	}
}

func dred16kDownmixSampleFloat32(in []float32, idx, channels, up int) float32 {
	if channels == 1 {
		return float32(dredFloatToInt16(float32(up)*in[idx])) + dredVerySmall
	}
	l := in[2*idx]
	r := in[2*idx+1]
	return float32(dredFloatToInt16(0.5*float32(up)*(l+r))) + dredVerySmall
}

func dredFilterZeroStuffedTo16kFloat32(dst []float32, in []float32, channels, up, workLen, keepEvery int, spec dred16kFilterSpec, mem *[ResamplingOrder + 1]float32) int {
	if mem == nil || keepEvery <= 0 {
		return 0
	}
	out := 0
	for i := range workLen {
		xi := float32(0)
		if i%up == 0 {
			xi = dred16kDownmixSampleFloat32(in, i/up, channels, up)
		}
		yi := dredFilterDF2TStep(xi, spec, mem)
		if i%keepEvery == 0 {
			if out >= len(dst) {
				return 0
			}
			dst[out] = yi
			out++
		}
	}
	if out != len(dst) {
		return 0
	}
	return out
}

func dredFilterDF2TStep(xi float32, spec dred16kFilterSpec, mem *[ResamplingOrder + 1]float32) float32 {
	yi := xi*spec.b0 + mem[0]
	nyi := -yi
	for j := range ResamplingOrder {
		mem[j] = mem[j+1] + spec.b[j]*xi + spec.a[j]*nyi
	}
	return yi
}

func dredFloatToInt16(v float32) int16 {
	return opusmath.Float32ToInt16(v)
}

package rdovae

import (
	"runtime"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dnnmath"
)

const (
	activationLinear = iota
	activationSigmoid
	activationTanh
)

const maxInputs = 2048

// Match the pinned libopus DNN kernels selected by the helper build. Linux
// parity helpers explicitly disable x86 intrinsics, so amd64 stays on the
// scalar path instead of simulating libopus' optional vector kernels. Keep
// these as constants so the unused arch branch folds away.
const (
	useArm64DNNVectorKernels = runtime.GOARCH == "arm64"
	useX86DNNVectorKernels   = false
	useNearestEvenQuant      = useArm64DNNVectorKernels || useX86DNNVectorKernels
)

type decoderState struct {
	initialized bool
	gru         [5][GRUStateSize]float32
	conv        [5][ConvStateSize]float32
}

type runtimeScratch struct {
	hidden    [HiddenInitOutSize]float32
	stateInit [GRUInitOutSize]float32
	buffer    [ProcessBufferSize]float32
	convTmp   [MaxConvInputs]float32
	convInput [MaxConvInputs]float32
	zrh       [3 * MaxRNNNeurons]float32
	recur     [3 * MaxRNNNeurons]float32
	act       [MaxRNNNeurons]float32
	quant     [maxInputs]int8
}

// Processor owns reusable DRED decoder runtime state and scratch so callers
// can keep DecodeAll on an explicit zero-allocation path across packets.
type Processor struct {
	state   decoderState
	scratch runtimeScratch
}

func (p *Processor) reset() {
	if p == nil {
		return
	}
	p.state = decoderState{}
}

// DecodeAll mirrors libopus DRED_rdovae_decode_all() and writes the retained
// feature frames into dst. It returns the number of floats written.
func (m *Decoder) DecodeAll(dst, state, latents []float32, nbLatents int) int {
	return m.DecodeAllWithProcessor(nil, dst, state, latents, nbLatents)
}

// DecodeAllWithProcessor mirrors libopus DRED_rdovae_decode_all() and reuses
// the caller-owned processor state/scratch when provided.
func (m *Decoder) DecodeAllWithProcessor(processor *Processor, dst, state, latents []float32, nbLatents int) int {
	if m == nil || nbLatents <= 0 {
		return 0
	}

	var local Processor
	if processor == nil {
		processor = &local
	}
	processor.reset()
	m.initStates(&processor.state, state, &processor.scratch)

	written := 0
	for i := range nbLatents {
		out := dst[written : written+OutputOutSize]
		in := latents[i*latentStride : (i+1)*latentStride]
		m.decodeQFrame(&processor.state, &processor.scratch, out, in)
		written += OutputOutSize
	}
	return written
}

func (m *Decoder) initStates(dec *decoderState, initialState []float32, scratch *runtimeScratch) {
	computeGenericDense(&m.HiddenInit, scratch.hidden[:], initialState[:StateDim], activationTanh, scratch)
	computeGenericDense(&m.GRUInit, scratch.stateInit[:], scratch.hidden[:], activationTanh, scratch)

	offset := 0
	for i := range dec.gru {
		copy(dec.gru[i][:], scratch.stateInit[offset:offset+GRUStateSize])
		offset += GRUStateSize
	}
	dec.initialized = false
}

func (m *Decoder) decodeQFrame(dec *decoderState, scratch *runtimeScratch, qframe, input []float32) {
	buffer := scratch.buffer[:]
	convTmp := scratch.convTmp[:]
	outputIndex := 0

	computeGenericDense(&m.Dense1, buffer[outputIndex:outputIndex+Dense1OutSize], input[:LatentDim+1], activationTanh, scratch)
	outputIndex += Dense1OutSize

	computeGenericGRU(&m.GRUInput[0], &m.GRURecur[0], dec.gru[0][:], buffer[:outputIndex], scratch)
	computeGLU(&m.GLU[0], buffer[outputIndex:outputIndex+GRUOutSize], dec.gru[0][:], scratch)
	outputIndex += GRUOutSize
	conv1CondInit(dec.conv[0][:], &dec.initialized)
	computeGenericDense(&m.ConvDense[0], convTmp[:ConvDenseOutSize], buffer[:outputIndex], activationTanh, scratch)
	computeGenericConv1D(&m.Conv[0], buffer[outputIndex:outputIndex+ConvOutSize], dec.conv[0][:], convTmp[:ConvDenseOutSize], ConvOutSize, activationTanh, scratch)
	outputIndex += ConvOutSize

	computeGenericGRU(&m.GRUInput[1], &m.GRURecur[1], dec.gru[1][:], buffer[:outputIndex], scratch)
	computeGLU(&m.GLU[1], buffer[outputIndex:outputIndex+GRUOutSize], dec.gru[1][:], scratch)
	outputIndex += GRUOutSize
	computeGenericDense(&m.ConvDense[1], convTmp[:ConvDenseOutSize], buffer[:outputIndex], activationTanh, scratch)
	computeGenericConv1D(&m.Conv[1], buffer[outputIndex:outputIndex+ConvOutSize], dec.conv[1][:], convTmp[:ConvDenseOutSize], ConvOutSize, activationTanh, scratch)
	outputIndex += ConvOutSize

	computeGenericGRU(&m.GRUInput[2], &m.GRURecur[2], dec.gru[2][:], buffer[:outputIndex], scratch)
	computeGLU(&m.GLU[2], buffer[outputIndex:outputIndex+GRUOutSize], dec.gru[2][:], scratch)
	outputIndex += GRUOutSize
	computeGenericDense(&m.ConvDense[2], convTmp[:ConvDenseOutSize], buffer[:outputIndex], activationTanh, scratch)
	computeGenericConv1D(&m.Conv[2], buffer[outputIndex:outputIndex+ConvOutSize], dec.conv[2][:], convTmp[:ConvDenseOutSize], ConvOutSize, activationTanh, scratch)
	outputIndex += ConvOutSize

	computeGenericGRU(&m.GRUInput[3], &m.GRURecur[3], dec.gru[3][:], buffer[:outputIndex], scratch)
	computeGLU(&m.GLU[3], buffer[outputIndex:outputIndex+GRUOutSize], dec.gru[3][:], scratch)
	outputIndex += GRUOutSize
	computeGenericDense(&m.ConvDense[3], convTmp[:ConvDenseOutSize], buffer[:outputIndex], activationTanh, scratch)
	computeGenericConv1D(&m.Conv[3], buffer[outputIndex:outputIndex+ConvOutSize], dec.conv[3][:], convTmp[:ConvDenseOutSize], ConvOutSize, activationTanh, scratch)
	outputIndex += ConvOutSize

	computeGenericGRU(&m.GRUInput[4], &m.GRURecur[4], dec.gru[4][:], buffer[:outputIndex], scratch)
	computeGLU(&m.GLU[4], buffer[outputIndex:outputIndex+GRUOutSize], dec.gru[4][:], scratch)
	outputIndex += GRUOutSize
	computeGenericDense(&m.ConvDense[4], convTmp[:ConvDenseOutSize], buffer[:outputIndex], activationTanh, scratch)
	computeGenericConv1D(&m.Conv[4], buffer[outputIndex:outputIndex+ConvOutSize], dec.conv[4][:], convTmp[:ConvDenseOutSize], ConvOutSize, activationTanh, scratch)
	outputIndex += ConvOutSize

	computeGenericDense(&m.Output, qframe[:OutputOutSize], buffer[:outputIndex], activationLinear, scratch)
}

func conv1CondInit(mem []float32, initialized *bool) {
	if *initialized {
		return
	}
	clear(mem)
	*initialized = true
}

func computeGenericDense(layer *LinearLayer, output, input []float32, activation int, scratch *runtimeScratch) {
	computeLinear(layer, output, input, scratch)
	computeActivation(output, output, layer.NbOutputs, activation)
}

func computeGenericGRU(inputWeights, recurrentWeights *LinearLayer, state, in []float32, scratch *runtimeScratch) {
	n := recurrentWeights.NbInputs
	zrh := scratch.zrh[:3*n]
	recur := scratch.recur[:3*n]
	z := zrh[:n]
	r := zrh[n : 2*n]
	h := zrh[2*n : 3*n]

	computeLinear(inputWeights, zrh[:3*n], in[:inputWeights.NbInputs], scratch)
	computeLinear(recurrentWeights, recur[:3*n], state[:n], scratch)
	for i := 0; i < 2*n; i++ {
		zrh[i] += recur[i]
	}
	computeActivation(zrh[:2*n], zrh[:2*n], 2*n, activationSigmoid)
	for i := range n {
		h[i] += recur[2*n+i] * r[i]
	}
	computeActivation(h, h, n, activationTanh)
	for i := range n {
		h[i] = z[i]*state[i] + (1-z[i])*h[i]
		state[i] = h[i]
	}
}

func computeGLU(layer *LinearLayer, output, input []float32, scratch *runtimeScratch) {
	n := layer.NbOutputs
	act := scratch.act[:n]
	computeLinear(layer, act[:n], input[:layer.NbInputs], scratch)
	computeActivation(act[:n], act[:n], n, activationSigmoid)
	for i := range n {
		output[i] = input[i] * act[i]
	}
}

func computeGenericConv1D(layer *LinearLayer, output, mem, input []float32, inputSize, activation int, scratch *runtimeScratch) {
	tmp := scratch.convInput[:layer.NbInputs]
	if layer.NbInputs != inputSize {
		copy(tmp[:layer.NbInputs-inputSize], mem[:layer.NbInputs-inputSize])
	}
	copy(tmp[layer.NbInputs-inputSize:layer.NbInputs], input[:inputSize])
	computeLinear(layer, output[:layer.NbOutputs], tmp[:layer.NbInputs], scratch)
	computeActivation(output, output, layer.NbOutputs, activation)
	if layer.NbInputs != inputSize {
		copy(mem[:layer.NbInputs-inputSize], tmp[inputSize:layer.NbInputs])
	}
}

func computeGenericConv1DDilation(layer *LinearLayer, output, mem, input []float32, inputSize, dilation, activation int, scratch *runtimeScratch) {
	tmp := scratch.convInput[:layer.NbInputs]
	kernelSize := layer.NbInputs / inputSize
	if dilation == 1 {
		if layer.NbInputs != inputSize {
			copy(tmp[:layer.NbInputs-inputSize], mem[:layer.NbInputs-inputSize])
		}
	} else {
		for i := 0; i < kernelSize-1; i++ {
			src := i * inputSize * dilation
			copy(tmp[i*inputSize:(i+1)*inputSize], mem[src:src+inputSize])
		}
	}
	copy(tmp[layer.NbInputs-inputSize:layer.NbInputs], input[:inputSize])
	computeLinear(layer, output[:layer.NbOutputs], tmp[:layer.NbInputs], scratch)
	computeActivation(output, output, layer.NbOutputs, activation)
	if dilation == 1 {
		if layer.NbInputs != inputSize {
			copy(mem[:layer.NbInputs-inputSize], tmp[inputSize:layer.NbInputs])
		}
		return
	}
	shift := inputSize*dilation*(kernelSize-1) - inputSize
	copy(mem[:shift], mem[inputSize:inputSize+shift])
	copy(mem[shift:shift+inputSize], input[:inputSize])
}

func computeLinear(layer *LinearLayer, out, in []float32, scratch *runtimeScratch) {
	bias := layer.Bias
	n := layer.NbOutputs
	m := layer.NbInputs

	if !layer.FloatWeights.Empty() {
		if !layer.WeightsIdx.Empty() {
			sparseSGEMV(out[:n], layer.FloatWeights, layer.WeightsIdx, in[:m])
		} else {
			sgemv(out[:n], layer.FloatWeights, n, m, n, in[:m])
		}
	} else if !layer.Weights.Empty() {
		if !layer.WeightsIdx.Empty() {
			sparseCGEMV8x4(out[:n], layer.Weights, layer.WeightsIdx, layer.Scale, n, m, in[:m], scratch.quant[:m])
		} else {
			cgemv8x4(out[:n], layer.Weights, layer.Scale, n, m, in[:m], scratch.quant[:m])
		}
	} else {
		clear(out[:n])
	}

	if !bias.Empty() {
		for i := range n {
			out[i] += bias.At(i)
		}
	}
}

func sgemv(out []float32, weights FloatTensor, rows, cols, colStride int, x []float32) {
	if useArm64DNNVectorKernels {
		sgemvFused(out, weights, rows, cols, colStride, x)
		return
	}
	sgemvSplit(out, weights, rows, cols, colStride, x)
}

func sgemvFused(out []float32, weights FloatTensor, rows, cols, colStride int, x []float32) {
	clear(out[:rows])
	for i := range rows {
		var sum float32
		for j := range cols {
			sum = fma32(weights.At(j*colStride+i), x[j], sum)
		}
		out[i] = sum
	}
}

func sgemvSplit(out []float32, weights FloatTensor, rows, cols, colStride int, x []float32) {
	clear(out[:rows])
	for i := range rows {
		var sum float32
		for j := range cols {
			sum += weights.At(j*colStride+i) * x[j]
		}
		out[i] = sum
	}
}

func sparseSGEMV(out []float32, weights FloatTensor, idx IntTensor, x []float32) {
	rows := len(out)
	clear(out)
	wOffset := 0
	idxPos := 0
	for row := 0; row < rows; row += 8 {
		colBlocks := int(idx.At(idxPos))
		idxPos++
		y := out[row : row+8]
		for range colBlocks {
			pos := int(idx.At(idxPos))
			idxPos++
			x0 := x[pos]
			x1 := x[pos+1]
			x2 := x[pos+2]
			x3 := x[pos+3]
			for k := range 8 {
				base := wOffset + k
				y[k] += weights.At(base)*x0 +
					weights.At(base+8)*x1 +
					weights.At(base+16)*x2 +
					weights.At(base+24)*x3
			}
			wOffset += SparseBlockSize
		}
	}
}

func cgemv8x4(out []float32, weights Int8Tensor, scale FloatTensor, rows, cols int, x []float32, q []int8) {
	for i := range cols {
		q[i] = quantizeInput(x[i])
	}
	if useNearestEvenQuant {
		for row := 0; row < rows; row += 8 {
			var acc0, acc1, acc2, acc3, acc4, acc5, acc6, acc7 int
			wOffset := row * cols
			for col := 0; col < cols; col += 4 {
				x0 := int(q[col])
				x1 := int(q[col+1])
				x2 := int(q[col+2])
				x3 := int(q[col+3])
				acc0 += int(weights.At(wOffset+0))*x0 + int(weights.At(wOffset+1))*x1 + int(weights.At(wOffset+2))*x2 + int(weights.At(wOffset+3))*x3
				acc1 += int(weights.At(wOffset+4))*x0 + int(weights.At(wOffset+5))*x1 + int(weights.At(wOffset+6))*x2 + int(weights.At(wOffset+7))*x3
				acc2 += int(weights.At(wOffset+8))*x0 + int(weights.At(wOffset+9))*x1 + int(weights.At(wOffset+10))*x2 + int(weights.At(wOffset+11))*x3
				acc3 += int(weights.At(wOffset+12))*x0 + int(weights.At(wOffset+13))*x1 + int(weights.At(wOffset+14))*x2 + int(weights.At(wOffset+15))*x3
				acc4 += int(weights.At(wOffset+16))*x0 + int(weights.At(wOffset+17))*x1 + int(weights.At(wOffset+18))*x2 + int(weights.At(wOffset+19))*x3
				acc5 += int(weights.At(wOffset+20))*x0 + int(weights.At(wOffset+21))*x1 + int(weights.At(wOffset+22))*x2 + int(weights.At(wOffset+23))*x3
				acc6 += int(weights.At(wOffset+24))*x0 + int(weights.At(wOffset+25))*x1 + int(weights.At(wOffset+26))*x2 + int(weights.At(wOffset+27))*x3
				acc7 += int(weights.At(wOffset+28))*x0 + int(weights.At(wOffset+29))*x1 + int(weights.At(wOffset+30))*x2 + int(weights.At(wOffset+31))*x3
				wOffset += SparseBlockSize
			}
			out[row+0] = float32(acc0) * scale.At(row+0)
			out[row+1] = float32(acc1) * scale.At(row+1)
			out[row+2] = float32(acc2) * scale.At(row+2)
			out[row+3] = float32(acc3) * scale.At(row+3)
			out[row+4] = float32(acc4) * scale.At(row+4)
			out[row+5] = float32(acc5) * scale.At(row+5)
			out[row+6] = float32(acc6) * scale.At(row+6)
			out[row+7] = float32(acc7) * scale.At(row+7)
		}
		return
	}
	clear(out[:rows])
	wOffset := 0
	for row := 0; row < rows; row += 8 {
		y := out[row : row+8]
		for col := 0; col < cols; col += 4 {
			x0 := int(q[col])
			x1 := int(q[col+1])
			x2 := int(q[col+2])
			x3 := int(q[col+3])
			for k := range 8 {
				base := wOffset + k*4
				y[k] += float32(int(weights.At(base))*x0 +
					int(weights.At(base+1))*x1 +
					int(weights.At(base+2))*x2 +
					int(weights.At(base+3))*x3)
			}
			wOffset += SparseBlockSize
		}
		for k := range 8 {
			y[k] *= scale.At(row + k)
		}
	}
}

func sparseCGEMV8x4(out []float32, weights Int8Tensor, idx IntTensor, scale FloatTensor, rows, cols int, x []float32, q []int8) {
	_ = cols
	for i := range x {
		q[i] = quantizeInput(x[i])
	}
	if useNearestEvenQuant {
		wOffset := 0
		idxPos := 0
		for row := 0; row < rows; row += 8 {
			var acc0, acc1, acc2, acc3, acc4, acc5, acc6, acc7 int
			colBlocks := int(idx.At(idxPos))
			idxPos++
			for range colBlocks {
				pos := int(idx.At(idxPos))
				idxPos++
				x0 := int(q[pos])
				x1 := int(q[pos+1])
				x2 := int(q[pos+2])
				x3 := int(q[pos+3])
				acc0 += int(weights.At(wOffset+0))*x0 + int(weights.At(wOffset+1))*x1 + int(weights.At(wOffset+2))*x2 + int(weights.At(wOffset+3))*x3
				acc1 += int(weights.At(wOffset+4))*x0 + int(weights.At(wOffset+5))*x1 + int(weights.At(wOffset+6))*x2 + int(weights.At(wOffset+7))*x3
				acc2 += int(weights.At(wOffset+8))*x0 + int(weights.At(wOffset+9))*x1 + int(weights.At(wOffset+10))*x2 + int(weights.At(wOffset+11))*x3
				acc3 += int(weights.At(wOffset+12))*x0 + int(weights.At(wOffset+13))*x1 + int(weights.At(wOffset+14))*x2 + int(weights.At(wOffset+15))*x3
				acc4 += int(weights.At(wOffset+16))*x0 + int(weights.At(wOffset+17))*x1 + int(weights.At(wOffset+18))*x2 + int(weights.At(wOffset+19))*x3
				acc5 += int(weights.At(wOffset+20))*x0 + int(weights.At(wOffset+21))*x1 + int(weights.At(wOffset+22))*x2 + int(weights.At(wOffset+23))*x3
				acc6 += int(weights.At(wOffset+24))*x0 + int(weights.At(wOffset+25))*x1 + int(weights.At(wOffset+26))*x2 + int(weights.At(wOffset+27))*x3
				acc7 += int(weights.At(wOffset+28))*x0 + int(weights.At(wOffset+29))*x1 + int(weights.At(wOffset+30))*x2 + int(weights.At(wOffset+31))*x3
				wOffset += SparseBlockSize
			}
			out[row+0] = float32(acc0) * scale.At(row+0)
			out[row+1] = float32(acc1) * scale.At(row+1)
			out[row+2] = float32(acc2) * scale.At(row+2)
			out[row+3] = float32(acc3) * scale.At(row+3)
			out[row+4] = float32(acc4) * scale.At(row+4)
			out[row+5] = float32(acc5) * scale.At(row+5)
			out[row+6] = float32(acc6) * scale.At(row+6)
			out[row+7] = float32(acc7) * scale.At(row+7)
		}
		return
	}
	clear(out[:rows])
	wOffset := 0
	idxPos := 0
	for row := 0; row < rows; row += 8 {
		colBlocks := int(idx.At(idxPos))
		idxPos++
		y := out[row : row+8]
		for range colBlocks {
			pos := int(idx.At(idxPos))
			idxPos++
			x0 := int(q[pos])
			x1 := int(q[pos+1])
			x2 := int(q[pos+2])
			x3 := int(q[pos+3])
			for k := range 8 {
				base := wOffset + k*4
				y[k] += float32(int(weights.At(base))*x0 +
					int(weights.At(base+1))*x1 +
					int(weights.At(base+2))*x2 +
					int(weights.At(base+3))*x3)
			}
			wOffset += SparseBlockSize
		}
		for k := range 8 {
			y[k] *= scale.At(row + k)
		}
	}
}

func computeActivation(output, input []float32, n, activation int) {
	switch activation {
	case activationSigmoid:
		dnnmath.SigmoidVectorApprox(output, input, n)
	case activationTanh:
		dnnmath.TanhVectorApprox(output, input, n)
	default:
		if len(output) == 0 || len(input) == 0 || &output[0] == &input[0] {
			return
		}
		copy(output[:n], input[:n])
	}
}

func quantizeInput(x float32) int8 {
	if useNearestEvenQuant {
		return dnnmath.Cgemv8x4QuantizeInput(x)
	}
	return dnnmath.Cgemv8x4QuantizeInputScalar(x)
}

// FloatTensor, IntTensor and Int8Tensor are convenience aliases for the
// dnnblob views over the float32, int32 and int8 weight records the RDOVAE
// runtime reads.
type (
	FloatTensor = dnnblob.Float32View
	IntTensor   = dnnblob.Int32View
	Int8Tensor  = dnnblob.Int8View
)

package lpcnetplc

import (
	"runtime"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dnnmath"
	"github.com/thesyncim/gopus/internal/opusmath"
)

// Match the DNN kernels of the instruction-paired libopus DRED build. The
// arm64 SIMD lane uses the NEON kernel arithmetic (nearest-even quantization
// and integer blocks); the arm64 scalar build quantizes with floor-half and
// accumulates float blocks. The amd64 Go SIMD lane uses the AVX2/FMA
// compute_linear and compute_conv2d that dnn/x86/x86_dnn_map.c selects; the
// other amd64 lanes use the generic C kernels.
const useArm64DNNVectorKernels = runtime.GOARCH == "arm64" && useNEONAnalysisKernels

var useX86DNNVectorKernels = dnnmath.X86VectorKernels

type predictorState struct {
	gru1 [GRU1Size]float32
	gru2 [GRU2Size]float32
}

type predictorScratch struct {
	tmp     [DenseInSize]float32
	zrh     [3 * GRU1Size]float32
	recur   [3 * GRU1Size]float32
	quant   [maxModelIn]int16
	quantSU [maxModelIn]uint8
}

// Predictor owns reusable PLC model state and scratch so callers can keep the
// post-DRED feature predictor on an explicit zero-allocation path.
type Predictor struct {
	model   *Model
	state   predictorState
	scratch predictorScratch
}

// SetModel binds a libopus-style PLC model blob and resets predictor state.
func (p *Predictor) SetModel(blob *dnnblob.Blob) error {
	model, err := LoadModel(blob)
	if err != nil {
		p.model = nil
		p.Reset()
		return err
	}
	p.model = model
	p.Reset()
	return nil
}

// SetModelPreservingState replaces the bound model without clearing predictor
// state, matching libopus USE_WEIGHTS_FILE reloads.
func (p *Predictor) SetModelPreservingState(blob *dnnblob.Blob) error {
	model, err := LoadModel(blob)
	if err != nil {
		return err
	}
	p.model = model
	return nil
}

// Loaded reports whether a PLC model is currently retained.
func (p *Predictor) Loaded() bool {
	return p != nil && p.model != nil
}

// Reset clears the recurrent predictor state but preserves the loaded model.
func (p *Predictor) Reset() {
	if p == nil {
		return
	}
	p.state = predictorState{}
}

func (p *Predictor) copyState(dst *predictorState) {
	if p == nil || dst == nil {
		return
	}
	*dst = p.state
}

func (p *Predictor) setState(src *predictorState) {
	if p == nil {
		return
	}
	if src == nil {
		p.state = predictorState{}
		return
	}
	p.state = *src
}

// Predict mirrors libopus compute_plc_pred(). It writes one feature vector into
// out and returns the number of floats written.
func (p *Predictor) Predict(out, in []float32) int {
	if p == nil || p.model == nil || len(out) < NumFeatures || len(in) < InputSize {
		return 0
	}
	computeGenericDense(&p.model.DenseIn, p.scratch.tmp[:], in[:InputSize], activationTanh, &p.scratch)
	computeGenericGRU(&p.model.GRU1In, &p.model.GRU1Rec, p.state.gru1[:], p.scratch.tmp[:], &p.scratch)
	computeGenericGRU(&p.model.GRU2In, &p.model.GRU2Rec, p.state.gru2[:], p.state.gru1[:], &p.scratch)
	computeGenericDense(&p.model.DenseOut, out[:NumFeatures], p.state.gru2[:], activationLinear, &p.scratch)
	return NumFeatures
}

// ConsumeFECOrPredict mirrors libopus get_fec_or_pred(). It consumes one queued
// concrete FEC feature vector when available, otherwise predicts one feature
// vector from the current recurrent state.
func (s *State) ConsumeFECOrPredict(p *Predictor, out []float32) bool {
	if s == nil || p == nil || !p.Loaded() || len(out) < NumFeatures {
		return false
	}
	if s.fecReadPos != s.fecFillPos && s.fecSkip == 0 {
		var discard [NumFeatures]float32
		var plcFeatures [InputSize]float32
		copy(out[:NumFeatures], s.fec[s.fecReadPos][:])
		s.fecReadPos++
		copy(plcFeatures[2*NumBands:], out[:NumFeatures])
		plcFeatures[2*NumBands+NumFeatures] = -1
		p.Predict(discard[:], plcFeatures[:])
		return true
	}
	var zeros [InputSize]float32
	p.Predict(out[:NumFeatures], zeros[:])
	if s.fecSkip > 0 {
		s.fecSkip--
	}
	return false
}

func computeGenericDense(layer *LinearLayer, output, input []float32, activation int, scratch *predictorScratch) {
	computeLinear(layer, output, input, scratch)
	computeActivation(output, output, layer.NbOutputs, activation)
}

func computeGenericGRU(inputWeights, recurrentWeights *LinearLayer, state, in []float32, scratch *predictorScratch) {
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
		// libopus compute_generic_gru() (dnn/nnet.c): "h[i] += recur[2*N+i]*r[i]"
		// is one C statement, fused by clang -ffp-contract=on into fma(recur, r, h).
		h[i] = fma32(recur[2*n+i], r[i], h[i])
	}
	computeActivation(h, h, n, activationTanh)
	for i := range n {
		// libopus dnn/nnet.c:compute_generic_gru rounds (1-z)*h before
		// fusing z*state into it. Keep that rounded product explicit so Go
		// does not select z*state as the non-fused product.
		h[i] = fma32(z[i], state[i], noFMA32Mul(1-z[i], h[i]))
		state[i] = h[i]
	}
}

func computeLinear(layer *LinearLayer, out, in []float32, scratch *predictorScratch) {
	computeLinearQuant(layer, out, in, scratch.quant[:], scratch.quantSU[:])
}

// computeLinearQuant mirrors libopus compute_linear() for the dense layers the
// PLC, FARGAN and PitchDNN models use. q and qSU are the caller's input
// quantization scratch for the generic/NEON and AVX2 integer kernels.
func computeLinearQuant(layer *LinearLayer, out, in []float32, q []int16, qSU []uint8) {
	bias := layer.Bias
	n := layer.NbOutputs
	m := layer.NbInputs

	if !layer.FloatWeights.Empty() {
		sgemv(out[:n], layer.FloatWeights, n, m, n, in[:m])
	} else if !layer.Weights.Empty() {
		if useX86DNNVectorKernels {
			// dnn/nnet_arch.h selects subias for integer matrices under
			// dnn/vec_avx.h's USE_SU_BIAS.
			dnnmath.CGEMV8x4X86(out[:n], layer.Weights, layer.Scale, n, m, in[:m], qSU[:m])
			bias = layer.Subias
		} else {
			cgemv8x4(out[:n], layer.Weights, layer.Scale, n, m, in[:m], q[:m])
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

func sgemv(out []float32, weights dnnblob.Float32View, rows, cols, colStride int, x []float32) {
	if useX86DNNVectorKernels {
		dnnmath.SGEMVX86(out, weights, rows, cols, colStride, x)
		return
	}
	if useFusedFloatDense() {
		// The selected NEON sgemv reduces its single-output gain layer with
		// rounded products. The scalar libopus compute_linear_c uses an ascending
		// FMADD chain for that layer, as it does for the other output counts.
		if rows == 1 && useNEONAnalysisKernels {
			sgemvSplit(out, weights, rows, cols, colStride, x)
		} else {
			sgemvFused(out, weights, rows, cols, colStride, x)
		}
		return
	}
	sgemvSplit(out, weights, rows, cols, colStride, x)
}

func sgemvFused(out []float32, weights dnnblob.Float32View, rows, cols, colStride int, x []float32) {
	clear(out[:rows])
	for i := range rows {
		var sum float32
		for j := range cols {
			w := weights.At(j*colStride + i)
			sum = fma32(w, x[j], sum)
		}
		out[i] = sum
	}
}

func sgemvSplit(out []float32, weights dnnblob.Float32View, rows, cols, colStride int, x []float32) {
	clear(out[:rows])
	for i := range rows {
		var sum float32
		for j := range cols {
			w := weights.At(j*colStride + i)
			sum = round32(sum + round32(w*x[j]))
		}
		out[i] = sum
	}
}

func cgemv8x4(out []float32, weights dnnblob.Int8View, scale dnnblob.Float32View, rows, cols int, x []float32, q []int16) {
	for i := range cols {
		q[i] = quantizeInput(x[i])
	}
	if useArm64DNNVectorKernels {
		cgemv8x4IntAccum(out, weights, scale, rows, cols, q)
		return
	}
	cgemv8x4FloatAccum(out, weights, scale, rows, cols, q)
}

func cgemv8x4IntAccum(out []float32, weights dnnblob.Int8View, scale dnnblob.Float32View, rows, cols int, q []int16) {
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
			wOffset += 32
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
}

func cgemv8x4FloatAccum(out []float32, weights dnnblob.Int8View, scale dnnblob.Float32View, rows, cols int, q []int16) {
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
			wOffset += 32
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

func quantizeInput(x float32) int16 {
	return quantizeInputWithOptions(x, useNearestEvenQuant())
}

func quantizeInputWithOptions(x float32, nearestEven bool) int16 {
	if nearestEven {
		// Match libopus NEON: multiply in float32, then round to nearest-even.
		scaled := float32(127 * x)
		q := int16(opusmath.RoundToEvenF32ToInt32(scaled))
		return int16(int8(q))
	}
	scaled := float32(127 * x)
	return int16(int8(opusmath.FloorHalfPlusF32ToInt32(scaled)))
}

func useNearestEvenQuant() bool {
	return useArm64DNNVectorKernels
}

func useFusedFloatDense() bool {
	// The ARM scalar compute_linear_c and selected NEON float dense kernels
	// both use an ascending FMA chain, independent of int8 quantization.
	return runtime.GOARCH == "arm64"
}

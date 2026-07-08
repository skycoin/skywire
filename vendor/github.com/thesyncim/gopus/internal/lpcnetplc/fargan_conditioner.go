package lpcnetplc

import (
	"errors"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/opusmath"
)

// FARGAN conditioning-network dimensions, copied verbatim from libopus 1.6.1
// dnn/fargan.h and dnn/fargan_data.h. The conditioning network embeds the
// pitch period, then runs two dense layers around a delayed conv1d to produce
// the per-subframe conditioning vectors that drive the FARGAN signal network.
// PitchMinPeriod / PitchMaxPeriod bound the pitch-period search shared with the
// pitch-prediction buffer.
const (
	FARGANPEmbedInputs     = 224
	FARGANPEmbedOutSize    = 12
	FARGANCondDense1Size   = 64
	FARGANCondConv1OutSize = 128
	FARGANCondConv1InSize  = 64
	FARGANCondConv1Delay   = 1
	FARGANCondConv1State   = FARGANCondConv1InSize * (FARGANCondConv1Delay + 1)
	FARGANCondConv1Inputs  = FARGANCondConv1State + FARGANCondConv1InSize
	FARGANCondDense2Size   = 320
	FARGANNBSubframes      = 4
	FARGANCondSize         = FARGANCondDense2Size / FARGANNBSubframes
	PitchMinPeriod         = 32
	PitchMaxPeriod         = 256
)

var errInvalidFARGANModel = errors.New("lpcnetplc: invalid fargan model")

// FARGANConditionerModel holds the FARGAN conditioning-network weight layers:
// the pitch embedding, two dense layers and the delayed conv1d, matching the
// cond_net members libopus binds in init_fargan (dnn/fargan_data.c).
type FARGANConditionerModel struct {
	PEmbed LinearLayer
	Dense1 LinearLayer
	Conv1  LinearLayer
	Dense2 LinearLayer
}

// FARGANConditionerState is the persistent conditioning-network state: the
// conv1d delay-line memory and the last pitch period, mirroring the cond
// members of libopus FARGANState.
type FARGANConditionerState struct {
	condConv1State [FARGANCondConv1State]float32
	lastPeriod     int
}

type farganConditionerScratch struct {
	denseIn  [NumFeatures + FARGANPEmbedOutSize]float32
	convIn   [FARGANCondDense1Size]float32
	fdense2  [FARGANCondConv1OutSize]float32
	convTemp [FARGANCondConv1Inputs]float32
	quant    [FARGANCondConv1Inputs]int16
}

// FARGANConditioner mirrors the libopus compute_fargan_cond() runtime and
// retains the conv1 state needed across frames on a zero-allocation path.
type FARGANConditioner struct {
	model   *FARGANConditionerModel
	state   FARGANConditionerState
	scratch farganConditionerScratch
}

var farganConditionerLayerSpecs = []LinearLayerSpec{
	{
		Name:         "cond_net_pembed",
		Bias:         "cond_net_pembed_bias",
		FloatWeights: "cond_net_pembed_weights_float",
		NbInputs:     FARGANPEmbedInputs,
		NbOutputs:    FARGANPEmbedOutSize,
	},
	{
		Name:         "cond_net_fdense1",
		Bias:         "cond_net_fdense1_bias",
		FloatWeights: "cond_net_fdense1_weights_float",
		NbInputs:     NumFeatures + FARGANPEmbedOutSize,
		NbOutputs:    FARGANCondDense1Size,
	},
	{
		Name:         "cond_net_fconv1",
		Bias:         "cond_net_fconv1_bias",
		Subias:       "cond_net_fconv1_subias",
		Weights:      "cond_net_fconv1_weights_int8",
		FloatWeights: "cond_net_fconv1_weights_float",
		Scale:        "cond_net_fconv1_scale",
		NbInputs:     FARGANCondConv1Inputs,
		NbOutputs:    FARGANCondConv1OutSize,
	},
	{
		Name:         "cond_net_fdense2",
		Bias:         "cond_net_fdense2_bias",
		Subias:       "cond_net_fdense2_subias",
		Weights:      "cond_net_fdense2_weights_int8",
		FloatWeights: "cond_net_fdense2_weights_float",
		Scale:        "cond_net_fdense2_scale",
		NbInputs:     FARGANCondConv1OutSize,
		NbOutputs:    FARGANCondDense2Size,
	},
}

// FARGANConditionerLayerSpecs returns the libopus-shaped FARGAN conditioning
// layer specs the pure-Go loader binds from a validated weights blob.
func FARGANConditionerLayerSpecs() []LinearLayerSpec {
	return farganConditionerLayerSpecs
}

// LoadFARGANConditionerModel binds the conditioning subset of the libopus
// FARGAN model family into typed Go layers.
func LoadFARGANConditionerModel(blob *dnnblob.Blob) (*FARGANConditionerModel, error) {
	if blob == nil {
		return nil, errInvalidFARGANModel
	}
	var model FARGANConditionerModel
	for _, spec := range farganConditionerLayerSpecs {
		layer, err := loadLinearLayer(blob, spec)
		if err != nil {
			return nil, errInvalidFARGANModel
		}
		switch spec.Name {
		case "cond_net_pembed":
			model.PEmbed = layer
		case "cond_net_fdense1":
			model.Dense1 = layer
		case "cond_net_fconv1":
			model.Conv1 = layer
		case "cond_net_fdense2":
			model.Dense2 = layer
		}
	}
	return &model, nil
}

// SetModel binds a validated libopus-style FARGAN blob and resets retained
// conditioner state.
func (c *FARGANConditioner) SetModel(blob *dnnblob.Blob) error {
	model, err := LoadFARGANConditionerModel(blob)
	if err != nil {
		c.model = nil
		c.Reset()
		return err
	}
	c.model = model
	c.Reset()
	return nil
}

// Reset clears the retained conditioning state but preserves the loaded model.
func (c *FARGANConditioner) Reset() {
	if c == nil {
		return
	}
	c.state = FARGANConditionerState{}
}

// LastPeriod reports the last period passed through ComputeWithPeriod.
func (c *FARGANConditioner) LastPeriod() int {
	if c == nil {
		return 0
	}
	return c.state.lastPeriod
}

// FillCondConv1State copies the retained conv1 conditioning state into dst.
func (c *FARGANConditioner) FillCondConv1State(dst []float32) int {
	if c == nil {
		return 0
	}
	n := min(len(c.state.condConv1State), len(dst))
	copy(dst[:n], c.state.condConv1State[:n])
	return n
}

// Compute mirrors libopus compute_fargan_cond() using the period derived from
// the current feature vector.
func (c *FARGANConditioner) Compute(out, features []float32) int {
	return c.ComputeWithPeriod(out, features, PeriodFromFeatures(features))
}

// ComputeWithPeriod mirrors libopus compute_fargan_cond() for an explicit pitch
// period. It writes one full 4-subframe conditioning vector.
func (c *FARGANConditioner) ComputeWithPeriod(out, features []float32, period int) int {
	if c == nil || c.model == nil || len(out) < FARGANCondDense2Size || len(features) < NumFeatures {
		return 0
	}
	slot := clampInt(period-PitchMinPeriod, 0, FARGANPEmbedInputs-1)
	copy(c.scratch.denseIn[:NumFeatures], features[:NumFeatures])
	for i := range FARGANPEmbedOutSize {
		c.scratch.denseIn[NumFeatures+i] = c.model.PEmbed.FloatWeights.At(slot*FARGANPEmbedOutSize + i)
	}
	computeFARGANDense(&c.model.Dense1, c.scratch.convIn[:], c.scratch.denseIn[:], activationTanh, &c.scratch)
	computeFARGANConv1D(&c.model.Conv1, c.scratch.fdense2[:], c.state.condConv1State[:], c.scratch.convIn[:], FARGANCondConv1InSize, activationTanh, &c.scratch)
	computeFARGANDense(&c.model.Dense2, out[:FARGANCondDense2Size], c.scratch.fdense2[:], activationTanh, &c.scratch)
	c.state.lastPeriod = period
	return FARGANCondDense2Size
}

// PeriodFromFeatures mirrors the libopus FARGAN pitch-period formula.
func PeriodFromFeatures(features []float32) int {
	if len(features) <= NumBands {
		return PitchMaxPeriod
	}
	periodF := float32(PitchMaxPeriod) / opusmath.Exp2F32(features[NumBands]+1.5)
	period := int(opusmath.FloorHalfPlusF32ToInt32(periodF))
	return period
}

func computeFARGANDense(layer *LinearLayer, output, input []float32, activation int, scratch *farganConditionerScratch) {
	computeFARGANLinear(layer, output, input, scratch)
	computeActivation(output, output, layer.NbOutputs, activation)
}

func computeFARGANConv1D(layer *LinearLayer, output, mem, input []float32, inputSize, activation int, scratch *farganConditionerScratch) {
	tmp := scratch.convTemp[:layer.NbInputs]
	if layer.NbInputs != inputSize {
		copy(tmp[:layer.NbInputs-inputSize], mem[:layer.NbInputs-inputSize])
	}
	copy(tmp[layer.NbInputs-inputSize:layer.NbInputs], input[:inputSize])
	computeFARGANLinear(layer, output[:layer.NbOutputs], tmp[:layer.NbInputs], scratch)
	computeActivation(output, output, layer.NbOutputs, activation)
	if layer.NbInputs != inputSize {
		copy(mem[:layer.NbInputs-inputSize], tmp[inputSize:layer.NbInputs])
	}
}

func computeFARGANLinear(layer *LinearLayer, out, in []float32, scratch *farganConditionerScratch) {
	bias := layer.Bias
	n := layer.NbOutputs
	m := layer.NbInputs

	if !layer.FloatWeights.Empty() {
		sgemv(out[:n], layer.FloatWeights, n, m, n, in[:m])
	} else if !layer.Weights.Empty() {
		cgemv8x4(out[:n], layer.Weights, layer.Scale, n, m, in[:m], scratch.quant[:m])
		if useSUBias && !layer.Subias.Empty() {
			bias = layer.Subias
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

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

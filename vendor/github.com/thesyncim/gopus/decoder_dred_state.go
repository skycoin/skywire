//go:build gopus_dred || gopus_osce

package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

type decoderDREDPayloadState struct {
	dredDNNBlob     *dnnblob.Blob
	dredData        []byte
	dredCache       internaldred.Cache
	dredDecoded     internaldred.Decoded
	dredModel       *rdovae.Decoder
	dredProcess     rdovae.Processor
	dredModelLoaded bool
}

type decoderDREDRecoveryState struct {
	dredPLC      lpcnetplc.State
	dredBlend    int
	dredRecovery int
}

type decoderDREDNeuralState struct {
	dredAnalysis  lpcnetplc.Analysis
	dredPredictor lpcnetplc.Predictor
	dredFARGAN    lpcnetplc.FARGAN
	dredPLCUpdate [4 * lpcnetplc.FrameSize]float32
	dredPLCRender [4 * lpcnetplc.FrameSize]float32

	dredRawHistoryUpdated bool

	pitchDNNLoaded    bool
	plcModelLoaded    bool
	farganModelLoaded bool
}

type decoderDRED48kBridgeState struct {
	dredPLCPCM        [4 * lpcnetplc.FrameSize]int16
	dredPLCFill       int
	dredPLCPreemphMem float32
	dredLastNeural    bool
}

type decoderDREDState struct {
	*decoderDREDPayloadState
	*decoderDREDRecoveryState
	*decoderDREDNeuralState
	*decoderDRED48kBridgeState
}

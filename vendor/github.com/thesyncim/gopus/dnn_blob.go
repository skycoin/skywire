//go:build gopus_dred || gopus_osce

// USE_WEIGHTS_FILE model-blob loading mirrors libopus's compile-gated DNN
// loaders. libopus only builds the rdovae/lpcnet/FARGAN loaders when a
// model-consuming runtime is enabled (ENABLE_DRED/ENABLE_OSCE/ENABLE_DEEP_PLC),
// so the clone-and-load helpers below compile only under the same tags. Default
// builds use the zero-cost SetDNNBlob no-op stubs and never link these loaders.

package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func cloneEncoderDNNBlobForControl(data []byte) (*dnnblob.Blob, error) {
	if data == nil {
		return nil, ErrInvalidArgument
	}
	blob, err := dnnblob.Clone(data)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	if err := blob.ValidateEncoderControl(); err != nil {
		return nil, ErrInvalidArgument
	}
	if _, err := rdovae.LoadEncoder(blob); err != nil {
		return nil, ErrInvalidArgument
	}
	if _, err := lpcnetplc.LoadPitchDNNModel(blob); err != nil {
		return nil, ErrInvalidArgument
	}
	return blob, nil
}

func cloneDecoderDNNBlobForControl(data []byte) (*dnnblob.Blob, error) {
	if data == nil {
		return nil, ErrInvalidArgument
	}
	blob, err := dnnblob.Clone(data)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	if err := blob.ValidateDecoderControl(false); err != nil {
		return nil, ErrInvalidArgument
	}
	if _, err := lpcnetplc.LoadPitchDNNModel(blob); err != nil {
		return nil, ErrInvalidArgument
	}
	if _, err := lpcnetplc.LoadModel(blob); err != nil {
		return nil, ErrInvalidArgument
	}
	if _, err := lpcnetplc.LoadFARGANModel(blob); err != nil {
		return nil, ErrInvalidArgument
	}
	return blob, nil
}

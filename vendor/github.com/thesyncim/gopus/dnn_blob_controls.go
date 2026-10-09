//go:build gopus_dred || gopus_osce

// USE_WEIGHTS_FILE control surface. libopus only exposes OPUS_SET_DNN_BLOB when
// a model-consuming runtime is compiled in (ENABLE_DRED/ENABLE_OSCE/
// ENABLE_DEEP_PLC). gopus compiles these model-loading SetDNNBlob controls with
// gopus_dred or gopus_osce; default builds return
// ErrOptionalExtensionUnavailable from dnn_blob_controls_default.go.

package gopus

// SetDNNBlob validates and loads the optional USE_WEIGHTS_FILE decoder model
// blob. It copies data before retaining it, returns ErrInvalidArgument for nil,
// malformed, or incompatible data, and retains the blob across Reset. This
// control is available with the gopus_dred or gopus_osce build tag.
func (d *Decoder) SetDNNBlob(data []byte) error {
	blob, err := cloneDecoderDNNBlobForControl(data)
	if err != nil {
		return err
	}
	if err := d.setDNNBlob(blob); err != nil {
		return ErrInvalidArgument
	}
	return nil
}

// SetDNNBlob validates and loads the optional USE_WEIGHTS_FILE encoder model
// blob. It copies data before retaining it, returns ErrInvalidArgument for nil,
// malformed, or incompatible data, and retains the blob across Reset. This
// control is available with the gopus_dred or gopus_osce build tag.
func (e *Encoder) SetDNNBlob(data []byte) error {
	blob, err := cloneEncoderDNNBlobForControl(data)
	if err != nil {
		return err
	}
	e.dnnBlob = blob
	e.enc.SetDNNBlob(blob)
	return nil
}

// SetDNNBlob validates and loads the optional USE_WEIGHTS_FILE encoder model
// blob. It copies data before retaining it, returns ErrInvalidArgument for nil,
// malformed, or incompatible data, and retains the blob across Reset. This
// control is available with the gopus_dred or gopus_osce build tag.
func (e *MultistreamEncoder) SetDNNBlob(data []byte) error {
	blob, err := cloneEncoderDNNBlobForControl(data)
	if err != nil {
		return err
	}
	e.dnnBlob = blob
	e.enc.SetDNNBlob(blob)
	return nil
}

// SetDNNBlob validates and loads the optional USE_WEIGHTS_FILE decoder model
// blob. It copies data before retaining it, returns ErrInvalidArgument for nil,
// malformed, or incompatible data, and retains the blob across Reset. This
// control is available with the gopus_dred or gopus_osce build tag.
func (d *MultistreamDecoder) SetDNNBlob(data []byte) error {
	blob, err := cloneDecoderDNNBlobForControl(data)
	if err != nil {
		return err
	}
	d.dnnBlob = blob
	d.dec.SetDNNBlob(blob)
	return nil
}

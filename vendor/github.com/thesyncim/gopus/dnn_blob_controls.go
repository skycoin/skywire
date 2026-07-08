//go:build gopus_dred || gopus_osce

// USE_WEIGHTS_FILE control surface. libopus only exposes OPUS_SET_DNN_BLOB when
// a model-consuming runtime is compiled in (ENABLE_DRED/ENABLE_OSCE/
// ENABLE_DEEP_PLC). gopus mirrors that by compiling the real, model-loading
// SetDNNBlob controls only under the DRED/extra-controls tags; default builds
// use the zero-cost no-op stubs in dnn_blob_controls_default.go.

package gopus

// SetDNNBlob loads the optional libopus USE_WEIGHTS_FILE decoder model blob.
//
// The loaded blob is validated using libopus-style weights-record framing and
// retained across Reset(), matching libopus USE_WEIGHTS_FILE control lifetime.
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

// SetDNNBlob loads the optional libopus USE_WEIGHTS_FILE encoder model blob.
//
// The loaded blob is validated using libopus-style weights-record framing and
// retained across Reset(), matching libopus USE_WEIGHTS_FILE control lifetime.
func (e *Encoder) SetDNNBlob(data []byte) error {
	blob, err := cloneEncoderDNNBlobForControl(data)
	if err != nil {
		return err
	}
	e.dnnBlob = blob
	e.enc.SetDNNBlob(blob)
	return nil
}

// SetDNNBlob loads the optional libopus USE_WEIGHTS_FILE encoder model blob.
//
// The loaded blob is validated using libopus-style weights-record framing and
// retained across Reset(), matching libopus USE_WEIGHTS_FILE control lifetime.
func (e *MultistreamEncoder) SetDNNBlob(data []byte) error {
	blob, err := cloneEncoderDNNBlobForControl(data)
	if err != nil {
		return err
	}
	e.dnnBlob = blob
	e.enc.SetDNNBlob(blob)
	return nil
}

// SetDNNBlob loads the optional libopus USE_WEIGHTS_FILE decoder model blob.
//
// The loaded blob is validated using libopus-style weights-record framing and
// retained across Reset(), matching libopus USE_WEIGHTS_FILE control lifetime.
func (d *MultistreamDecoder) SetDNNBlob(data []byte) error {
	blob, err := cloneDecoderDNNBlobForControl(data)
	if err != nil {
		return err
	}
	d.dnnBlob = blob
	d.dec.SetDNNBlob(blob)
	return nil
}

//go:build !gopus_dred && !gopus_osce

// Default-build USE_WEIGHTS_FILE control stubs.
//
// libopus keeps every DNN/model loader behind compile flags
// (ENABLE_DRED/ENABLE_OSCE/ENABLE_DEEP_PLC); a default build links none of
// them and OPUS_SET_DNN_BLOB is not part of the surface. gopus mirrors that:
// the public SetDNNBlob method name stays on every type so the API contract is
// build-invariant, but in default builds it is a zero-cost no-op that loads no
// model and never references the rdovae/lpcnet/FARGAN loaders, so those never
// link through this path. It reports ErrOptionalExtensionUnavailable, matching
// the other default optional-extension control stubs.

package gopus

// SetDNNBlob reports that USE_WEIGHTS_FILE model loading is unavailable in the
// default build. Build with -tags gopus_dred or -tags gopus_osce to
// enable the model-loading control.
func (d *Decoder) SetDNNBlob(_ []byte) error {
	return ErrOptionalExtensionUnavailable
}

// SetDNNBlob reports that USE_WEIGHTS_FILE model loading is unavailable in the
// default build. Build with -tags gopus_dred or -tags gopus_osce to
// enable the model-loading control.
func (e *Encoder) SetDNNBlob(_ []byte) error {
	return ErrOptionalExtensionUnavailable
}

// SetDNNBlob reports that USE_WEIGHTS_FILE model loading is unavailable in the
// default build. Build with -tags gopus_dred or -tags gopus_osce to
// enable the model-loading control.
func (e *MultistreamEncoder) SetDNNBlob(_ []byte) error {
	return ErrOptionalExtensionUnavailable
}

// SetDNNBlob reports that USE_WEIGHTS_FILE model loading is unavailable in the
// default build. Build with -tags gopus_dred or -tags gopus_osce to
// enable the model-loading control.
func (d *MultistreamDecoder) SetDNNBlob(_ []byte) error {
	return ErrOptionalExtensionUnavailable
}

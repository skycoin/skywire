//go:build !gopus_dred && !gopus_osce

// Default-build USE_WEIGHTS_FILE control stubs.
//
// libopus keeps every DNN/model loader behind compile flags
// (ENABLE_DRED/ENABLE_OSCE/ENABLE_DEEP_PLC); a default build links none of
// them and OPUS_SET_DNN_BLOB is not part of the libopus surface. gopus keeps
// SetDNNBlob methods available on its public types in every build, but these
// default-build stubs return ErrOptionalExtensionUnavailable without loading a
// model or linking the rdovae/lpcnet/FARGAN loaders.

package gopus

// SetDNNBlob returns ErrOptionalExtensionUnavailable in the default build.
// Build with -tags gopus_dred or -tags gopus_osce to enable model loading.
func (d *Decoder) SetDNNBlob(_ []byte) error {
	return ErrOptionalExtensionUnavailable
}

// SetDNNBlob returns ErrOptionalExtensionUnavailable in the default build.
// Build with -tags gopus_dred or -tags gopus_osce to enable model loading.
func (e *Encoder) SetDNNBlob(_ []byte) error {
	return ErrOptionalExtensionUnavailable
}

// SetDNNBlob returns ErrOptionalExtensionUnavailable in the default build.
// Build with -tags gopus_dred or -tags gopus_osce to enable model loading.
func (e *MultistreamEncoder) SetDNNBlob(_ []byte) error {
	return ErrOptionalExtensionUnavailable
}

// SetDNNBlob returns ErrOptionalExtensionUnavailable in the default build.
// Build with -tags gopus_dred or -tags gopus_osce to enable model loading.
func (d *MultistreamDecoder) SetDNNBlob(_ []byte) error {
	return ErrOptionalExtensionUnavailable
}

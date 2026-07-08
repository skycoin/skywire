package gopus

import "github.com/thesyncim/gopus/internal/extsupport"

// OptionalExtension identifies a recognized libopus build-time extension surface.
type OptionalExtension string

const (
	// OptionalExtensionDRED identifies DRED controls and standalone DRED packet
	// helpers, which are supported only when built with -tags gopus_dred.
	OptionalExtensionDRED OptionalExtension = "dred"

	// OptionalExtensionDNNBlob identifies supported weights-file model blob loading.
	OptionalExtensionDNNBlob OptionalExtension = "dnn_blob"

	// OptionalExtensionQEXT identifies the optional extended-precision theta path,
	// which is supported only when built with -tags gopus_qext.
	OptionalExtensionQEXT OptionalExtension = "qext"

	// OptionalExtensionOSCEBWE identifies decoder-side OSCE BWE controls, which
	// remain parity-only extra controls rather than supported release surface.
	OptionalExtensionOSCEBWE OptionalExtension = "osce_bwe"
)

// SupportsOptionalExtension reports whether the current build makes ext part of
// the supported release surface for this build. Extra controls may be compiled
// for parity work while still reporting false here.
func SupportsOptionalExtension(ext OptionalExtension) bool {
	switch ext {
	case OptionalExtensionDRED:
		return extsupport.DRED
	case OptionalExtensionDNNBlob:
		return extsupport.DNNBlob
	case OptionalExtensionQEXT:
		return extsupport.QEXT
	case OptionalExtensionOSCEBWE:
		return extsupport.OSCEBWE
	default:
		return false
	}
}

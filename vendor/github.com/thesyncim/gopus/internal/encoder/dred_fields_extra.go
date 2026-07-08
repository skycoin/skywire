//go:build gopus_dred || gopus_osce

package encoder

type encoderDREDFields struct {
	// dred owns optional DRED encoder controls/runtime.
	dred *dredEncoderExtras
}

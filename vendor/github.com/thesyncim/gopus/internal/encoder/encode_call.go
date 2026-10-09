package encoder

// EncodeInputFormat identifies the libopus public encode API used for a call.
// The conversion wrappers reject an invalid selected frame before entering
// opus_encode_native, while the native-format wrapper clears rangeFinal there.
type EncodeInputFormat uint8

const (
	EncodeInputFloat32 EncodeInputFormat = iota
	EncodeInputInt16
	EncodeInputInt24
)

// BeginEncodeCall applies the rangeFinal side effect of entering the matching
// libopus encode wrapper. Call it after validating the Go slice shape and
// before returning an expert-frame or output-budget error.
func (e *Encoder) BeginEncodeCall(input EncodeInputFormat, frameSize int) {
	if frameSize > 0 ||
		(!fixedPointBuild && input == EncodeInputFloat32) ||
		(fixedPointBuild && input == EncodeInputInt24) {
		e.finalRange = 0
	}
}

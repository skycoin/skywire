//go:build !gopus_osce

package gopus

// decoderOSCELACEState is an empty placeholder under the default build so
// the `*decoderOSCELACEState` field on Decoder compiles without dragging
// in the osce/lace package. The full struct lives in
// `decoder_osce_lace_state.go` under the `gopus_osce`
// build tag.
type decoderOSCELACEState struct{}

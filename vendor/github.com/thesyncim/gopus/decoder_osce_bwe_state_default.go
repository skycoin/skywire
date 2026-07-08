//go:build !gopus_osce

package gopus

// decoderOSCEBWEState is an empty placeholder under the default build so the
// `*decoderOSCEBWEState` field on Decoder compiles without dragging in the
// osce/bwe package. The full struct lives in `decoder_osce_bwe_state.go`
// under the `gopus_osce` build tag.
type decoderOSCEBWEState struct{}

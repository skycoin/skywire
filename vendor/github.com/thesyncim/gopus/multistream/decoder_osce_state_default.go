//go:build !gopus_osce

package multistream

// streamOSCEState is an empty placeholder under the default build so the
// `*streamOSCEState` field on `streamState` compiles without dragging in the
// extra-control OSCE runtime. The single-stream decoder uses the same dual-file
// pattern (`decoder_osce_bwe_state.go` / `decoder_osce_bwe_state_default.go`).
type streamOSCEState struct{}

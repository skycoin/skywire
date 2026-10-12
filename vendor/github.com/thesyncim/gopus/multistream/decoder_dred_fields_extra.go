//go:build gopus_dred || gopus_osce

package multistream

import (
	"github.com/thesyncim/gopus/internal/silk"
)

type decoderDREDFields struct {
	dred *decoderDREDState

	// rawSILKHistory retains native 16 kHz channel-0 PCM updates per child
	// decoder, including good frames decoded before the main PLC model loads.
	rawSILKHistory     [][]int16
	rawSILKHistoryPos  []int
	rawSILKHistoryFill []int
	pcmHistorySynced   []bool
	directRawCapture   []bool
	rawSILKFrameHooks  []silk.RawMonoFrameHook
	rawSILKLossHooks   []silk.RawMonoFrameHook
	deepPLCLossHooks   []silk.DeepPLCLossMonoHook
	deepPLCHookUsed    []bool
	dredGenerateHooks  []func([]float32) bool
}

//go:build gopus_dred || gopus_osce

package gopus

import (
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	"github.com/thesyncim/gopus/internal/silk"
)

type decoderDREDFields struct {
	dred *decoderDREDState

	// rawSILKHistory retains the PCM passed to lpcnet_plc_update without loading
	// the neural runtime. libopus updates it on each good 16 kHz SILK frame and
	// on classical loss frames when the PLC model is loaded.
	rawSILKHistory       [lpcnetplc.PLCBufSize]int16
	rawSILKHistoryPos    int
	rawSILKHistoryFill   int
	pcmHistorySynced     bool
	directRawCapture     bool
	rawSILKFrameHook     silk.RawMonoFrameHook
	rawSILKLossFrameHook silk.RawMonoFrameHook
	deepPLCLossHook      silk.DeepPLCLossMonoHook
	deepPLCHookUsed      bool
}

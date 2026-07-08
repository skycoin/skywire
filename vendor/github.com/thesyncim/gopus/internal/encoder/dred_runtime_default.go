//go:build !gopus_dred && !gopus_osce

package encoder

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/types"
)

type dredEmissionPlan struct {
	bitrate int32
}

func (e *Encoder) SetDNNBlob(blob *dnnblob.Blob) {
	e.dnnBlob = blob
}

func (e *Encoder) dredEncodingActive() bool {
	return false
}

func (e *Encoder) resetDREDControls() {}

func (e *Encoder) clearInactiveDREDHistory() {}

func (e *Encoder) processDREDLatentsWithActivity(_ []opusRes, _ int, _ bool) int {
	return 0
}

func (e *Encoder) backfillDREDActivityForFrame(_ int, _ bool) {}

func (e *Encoder) processDREDLatentsForPacket(_ []opusRes, _ int, _ int, _ Mode) int {
	return 0
}

func (e *Encoder) snapshotDREDPacketState() {}

func (e *Encoder) clearDREDPacketSnapshot() {}

func (e *Encoder) computeDREDEmissionPlan(_ int) (dredEmissionPlan, bool) {
	return dredEmissionPlan{}, false
}

func (e *Encoder) hybridDREDPrimaryBudget(_ int, _ int, _ dredEmissionPlan) int {
	return 0
}

func (e *Encoder) maybeBuildSingleFrameDREDPacket(_ []byte, _ Mode, _ types.Bandwidth, _ int, _ bool, _ []packetExtension) ([]byte, bool, error) {
	return nil, false, nil
}

func (e *Encoder) maybeBuildMultiFrameDREDPacket(_ [][]byte, _ Mode, _ types.Bandwidth, _ int, _ int, _ int, _ bool, _ bool, _ []packetExtension) ([]byte, bool, error) {
	return nil, false, nil
}

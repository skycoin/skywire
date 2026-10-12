//go:build !gopus_fixed_point

package multistream

// streamFixedFields is empty in the default build so the per-stream decoder
// state carries no FIXED_POINT integer CELT bookkeeping. The struct is embedded
// in streamState; an empty struct contributes zero size, keeping the default
// build byte-identical and the feature truly zero-cost.
type streamFixedFields struct{}

type decoderFixedFields struct{}

// setFixedRedundancy is a no-op in the default build because the float
// decoder handles redundancy directly.
func (*streamState) setFixedRedundancy(bool, bool, []byte, int) {}

func (*streamState) captureFixedSILKMain([]float32) {}

func fixedCELTCodedChannels(packetStereo bool) int {
	if packetStereo {
		return 2
	}
	return 1
}

func (*streamState) resetFixedDecoderState() {}

func (*streamState) beginFixedHybridPLCCapture(int) {}

func (*streamState) endFixedHybridPLCCapture() {}

func (d *streamState) decodeHybridPLCChunkToFloat32(frameSize int, out []float32) error {
	return d.hybridDec.DecodePLCToFloat32WithPacketStereoInto(frameSize, d.lastPacketStereo, out)
}

func (d *streamState) decodeHybridTransitionPLCToFloat32(frameSize int, out []float32) error {
	return d.hybridDec.DecodePLCToFloat32WithPacketStereoInto(frameSize, d.lastPacketStereo, out)
}

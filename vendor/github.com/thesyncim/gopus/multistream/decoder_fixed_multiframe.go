//go:build gopus_fixed_point

package multistream

type streamFixedMultiframeFields struct {
	fixedMultiframeParser  packetScratch
	fixedMultiframeBuilder packetScratch
	fixedMultiframeQEXT    streamQEXTPayloads
	fixedMultiframePacket  []byte
	fixedMultiframePCM     []int32
}

// decodeMultiframeToResFixed follows opus_decode_native's per-frame
// opus_decode_frame loop. Each child applies integer redundancy, transitions,
// and gain before its samples are copied to the complete packet output.
func (d *streamState) decodeMultiframeToResFixed(data []byte, frameSize int) ([]int32, bool, error) {
	parsed, err := parseOpusPacketInto(&d.fixedMultiframeParser, data, false)
	if err != nil || len(parsed.frames) == 0 || frameSize%len(parsed.frames) != 0 {
		return nil, false, ErrInvalidPacket
	}
	channels := int(d.channels)
	needed := frameSize * channels
	if cap(d.fixedMultiframePCM) < needed {
		d.fixedMultiframePCM = make([]int32, needed)
	}
	d.fixedMultiframePCM = d.fixedMultiframePCM[:needed]
	// Reserve the child-packet envelope, including extension framing. Each
	// active QEXT payload remains attached to its original elementary frame.
	reserve := max(msFrameTmp, len(data)+8)
	if cap(d.fixedMultiframePacket) < reserve {
		d.fixedMultiframePacket = make([]byte, reserve)
	}
	d.fixedMultiframeQEXT.collect(parsed.padding, parsed.paddingFrameCount, qextPacketExtensionID)
	subFrameSize := frameSize / len(parsed.frames)
	for i, payload := range parsed.frames {
		packet := d.fixedMultiframePacket[:len(payload)+1]
		packet[0] = data[0] &^ 3
		copy(packet[1:], payload)
		mode := parseStreamTOC(data[0]).mode
		if side := d.fixedMultiframeQEXT.frame(i); len(side) > 0 && (mode == streamModeCELT || mode == streamModeHybrid) && !d.ignoreExtensions {
			frames := [1][]byte{payload}
			extensions := [1]packetExtensionData{{ID: qextPacketExtensionID, Frame: 0, Data: side}}
			n, err := buildOpusPacketFromFramesAndExtensionsInto(&d.fixedMultiframeBuilder,
				parsed.tocBase, frames[:], extensions[:], false, d.fixedMultiframePacket)
			if err != nil {
				return nil, false, err
			}
			packet = d.fixedMultiframePacket[:n]
		}
		pcm, handled, err := d.decodePacketToResFixed(packet, subFrameSize)
		if err != nil {
			return nil, false, err
		}
		if !handled || len(pcm) != subFrameSize*channels {
			return nil, false, ErrInvalidPacket
		}
		copy(d.fixedMultiframePCM[i*subFrameSize*channels:], pcm)
	}
	d.recordDecodeCall(frameSize, len(data))
	return d.fixedMultiframePCM, true, nil
}

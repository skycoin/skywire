//go:build gopus_dred || gopus_osce

package multistream

import internaldred "github.com/thesyncim/gopus/internal/dred"

func findDREDPayload(packet []byte) (payload []byte, frameOffset int, ok bool, err error) {
	parsed, err := parseOpusPacket(packet, false)
	if err != nil {
		return nil, 0, false, err
	}
	if len(parsed.padding) == 0 || parsed.paddingFrameCount <= 0 {
		return nil, 0, false, nil
	}

	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, parsed.padding, parsed.paddingFrameCount)
	frameSize := getFrameDuration(packet)
	if parsed.paddingFrameCount > 0 {
		frameSize /= parsed.paddingFrameCount
	}

	for {
		var ext packetExtensionData
		more, iterErr := iter.next(&ext)
		if iterErr != nil {
			return nil, 0, false, iterErr
		}
		if !more {
			return nil, 0, false, nil
		}
		if ext.ID != internaldred.ExtensionID {
			continue
		}
		if !internaldred.ValidExperimentalPayload(ext.Data) {
			continue
		}
		return ext.Data[internaldred.ExperimentalHeaderBytes:], ext.Frame * frameSize / 120, true, nil
	}
}

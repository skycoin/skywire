//go:build gopus_qext

package multistream

type streamQEXTPayloads struct {
	payloads [maxPacketExtensionFrames][]byte
}

func (p *streamQEXTPayloads) frame(frameIndex int) []byte {
	if p == nil || frameIndex < 0 || frameIndex >= len(p.payloads) {
		return nil
	}
	return p.payloads[frameIndex]
}

func (p *streamQEXTPayloads) collect(data []byte, nbFrames, id int) {
	if p == nil {
		return
	}
	collectQEXTPacketExtensions(data, nbFrames, id, &p.payloads)
}

func collectQEXTPacketExtensions(data []byte, nbFrames, id int, payloads *[maxPacketExtensionFrames][]byte) {
	if payloads == nil {
		return
	}
	for i := 0; i < maxPacketExtensionFrames; i++ {
		payloads[i] = nil
	}
	if len(data) == 0 || nbFrames <= 0 {
		return
	}

	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, data, nbFrames)
	for {
		var ext packetExtensionData
		ok, err := iter.next(&ext)
		if err != nil || !ok {
			return
		}
		if ext.ID != id || ext.Frame < 0 || ext.Frame >= nbFrames {
			continue
		}
		if payloads[ext.Frame] == nil {
			payloads[ext.Frame] = ext.Data
		}
	}
}

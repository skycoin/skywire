package gopus

func lastMultistreamPacketOffset(src []byte, length, numStreams int) (int, error) {
	offset := 0
	for s := 0; s < numStreams-1; s++ {
		_, _, consumed, err := parseSelfDelimitedPacket(src[offset:length])
		if err != nil {
			return 0, err
		}
		offset += consumed
	}
	if offset >= length {
		return 0, ErrInvalidPacket
	}
	return offset, nil
}

func decodeMultistreamPacket(src []byte, srcOffset, length int, selfDelimited bool) ([]byte, int, error) {
	if selfDelimited {
		return decodeSelfDelimitedPacket(src[srcOffset:length])
	}
	if srcOffset >= length {
		return nil, 0, ErrInvalidPacket
	}
	return src[srcOffset:length], length - srcOffset, nil
}

// MultistreamPacketPad pads the final stream packet inside a multistream packet.
func MultistreamPacketPad(data []byte, length, newLen, numStreams int) error {
	if numStreams < 1 || length < 1 || newLen < length {
		return ErrInvalidArgument
	}
	if length > len(data) {
		return ErrInvalidArgument
	}
	if newLen > cap(data) {
		return ErrBufferTooSmall
	}
	if newLen == length {
		return nil
	}

	src := make([]byte, length)
	copy(src, data[:length])

	offset, err := lastMultistreamPacketOffset(src, length, numStreams)
	if err != nil {
		return err
	}

	data = data[:newLen]
	copy(data[:length], src)

	lastOldLen := length - offset
	lastNewLen := lastOldLen + (newLen - length)
	return PacketPad(data[offset:], lastOldLen, lastNewLen)
}

// MultistreamPacketUnpad removes padding from all streams in a multistream packet.
// It returns the new packet length.
func MultistreamPacketUnpad(data []byte, length, numStreams int) (int, error) {
	if numStreams < 1 || length < 1 || length > len(data) {
		return 0, ErrInvalidArgument
	}

	src := make([]byte, length)
	copy(src, data[:length])

	srcOffset := 0
	dstOffset := 0
	for s := range numStreams {
		selfDelimited := s < numStreams-1

		packet, consumed, err := decodeMultistreamPacket(src, srcOffset, length, selfDelimited)
		if err != nil {
			return 0, err
		}
		srcOffset += consumed

		packetCopy := make([]byte, len(packet))
		copy(packetCopy, packet)
		newPacketLen, err := PacketUnpad(packetCopy, len(packetCopy))
		if err != nil {
			return 0, err
		}

		if selfDelimited {
			selfDelimitedPacket, err := makeSelfDelimitedPacket(packetCopy[:newPacketLen])
			if err != nil {
				return 0, err
			}
			if dstOffset+len(selfDelimitedPacket) > len(data) {
				return 0, ErrBufferTooSmall
			}
			copy(data[dstOffset:], selfDelimitedPacket)
			dstOffset += len(selfDelimitedPacket)
			continue
		}

		if dstOffset+newPacketLen > len(data) {
			return 0, ErrBufferTooSmall
		}

		copy(data[dstOffset:], packetCopy[:newPacketLen])
		dstOffset += newPacketLen
	}

	return dstOffset, nil
}

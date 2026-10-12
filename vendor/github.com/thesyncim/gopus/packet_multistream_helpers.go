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

// MultistreamPacketPad pads the final stream packet in a multistream packet
// in place.
// length is the current packet length in bytes, newLen is the target length in
// bytes, and numStreams is the number of constituent Opus streams. The input
// slice must contain length bytes and have capacity for newLen bytes. If its
// original length is shorter, reslice the caller's slice to newLen after success.
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

// MultistreamPacketUnpad removes padding from every stream in a multistream packet
// in place and returns the new packet length in bytes. length is the number of
// input bytes and numStreams is the number of constituent Opus streams; all but
// the final stream packet must use self-delimited framing. data must contain
// length bytes. The function does not extend data, so len(data) must also be
// large enough for the rebuilt output.
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

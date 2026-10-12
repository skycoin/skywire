package gopus

const maxPacketBytesPerStream = 4000

// copyEncodedPacket copies a complete encoded packet into data. A nil packet
// produces a zero-byte result; a short destination returns ErrBufferTooSmall
// without copying a partial packet.
func copyEncodedPacket(packet, data []byte) (int, error) {
	if packet == nil {
		return 0, nil
	}
	if len(packet) > len(data) {
		return 0, ErrBufferTooSmall
	}
	copy(data, packet)
	return len(packet), nil
}

// encodeToOwnedPacket encodes into a temporary packet buffer and returns a
// slice backed by that allocation. The returned packet remains valid after
// subsequent encoder calls.
func encodeToOwnedPacket(size int, encode func([]byte) (int, error)) ([]byte, error) {
	data := make([]byte, size)
	n, err := encode(data)
	if err != nil {
		return nil, err
	}
	return data[:n], nil
}

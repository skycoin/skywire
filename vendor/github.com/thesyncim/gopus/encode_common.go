package gopus

const maxPacketBytesPerStream = 4000

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

func encodeToOwnedPacket(size int, encode func([]byte) (int, error)) ([]byte, error) {
	data := make([]byte, size)
	n, err := encode(data)
	if err != nil {
		return nil, err
	}
	return data[:n], nil
}

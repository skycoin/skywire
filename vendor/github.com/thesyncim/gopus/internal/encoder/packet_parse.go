package encoder

import "errors"

var (
	errPacketTooShort = errors.New("encoder: packet too short")
	errInvalidPacket  = errors.New("encoder: invalid packet")
)

func parsePacketFrames(packet []byte) (byte, [][]byte, error) {
	if len(packet) < 1 {
		return 0, nil, errPacketTooShort
	}

	toc := packet[0]
	frameCode := toc & 0x03

	switch frameCode {
	case 0:
		return toc, [][]byte{packet[1:]}, nil
	case 1:
		frameDataLen := len(packet) - 1
		if frameDataLen%2 != 0 {
			return 0, nil, errInvalidPacket
		}
		frameLen := frameDataLen / 2
		if 1+2*frameLen > len(packet) {
			return 0, nil, errInvalidPacket
		}
		return toc, [][]byte{
			packet[1 : 1+frameLen],
			packet[1+frameLen : 1+2*frameLen],
		}, nil
	case 2:
		if len(packet) < 2 {
			return 0, nil, errPacketTooShort
		}
		frame1Len, bytesRead, err := parseFrameLength(packet, 1)
		if err != nil {
			return 0, nil, err
		}
		headerLen := 1 + bytesRead
		if headerLen+frame1Len > len(packet) {
			return 0, nil, errInvalidPacket
		}
		frame2Len := len(packet) - headerLen - frame1Len
		if frame2Len < 0 {
			return 0, nil, errInvalidPacket
		}
		return toc, [][]byte{
			packet[headerLen : headerLen+frame1Len],
			packet[headerLen+frame1Len:],
		}, nil
	case 3:
		if len(packet) < 2 {
			return 0, nil, errPacketTooShort
		}
		countByte := packet[1]
		vbr := (countByte & 0x80) != 0
		hasPadding := (countByte & 0x40) != 0
		m := int(countByte & 0x3F)
		if m == 0 || m > 48 {
			return 0, nil, ErrInvalidFrameCount
		}

		offset := 2
		padding := 0
		if hasPadding {
			for {
				if offset >= len(packet) {
					return 0, nil, errPacketTooShort
				}
				padByte := int(packet[offset])
				offset++
				if padByte == 255 {
					padding += 254
				} else {
					padding += padByte
					break
				}
			}
		}

		dataEnd := len(packet) - padding
		if dataEnd < offset {
			return 0, nil, errInvalidPacket
		}

		frames := make([][]byte, m)
		if vbr {
			lengths := make([]int, m-1)
			for i := 0; i < m-1; i++ {
				frameLen, bytesRead, err := parseFrameLength(packet, offset)
				if err != nil {
					return 0, nil, err
				}
				offset += bytesRead
				lengths[i] = frameLen
			}
			dataOffset := offset
			for i := 0; i < m-1; i++ {
				frameLen := lengths[i]
				if dataOffset+frameLen > dataEnd {
					return 0, nil, errInvalidPacket
				}
				frames[i] = packet[dataOffset : dataOffset+frameLen]
				dataOffset += frameLen
			}
			lastLen := dataEnd - dataOffset
			if lastLen < 0 {
				return 0, nil, errInvalidPacket
			}
			frames[m-1] = packet[dataOffset : dataOffset+lastLen]
		} else {
			frameDataLen := dataEnd - offset
			if frameDataLen < 0 {
				return 0, nil, errInvalidPacket
			}
			if frameDataLen%m != 0 {
				return 0, nil, errInvalidPacket
			}
			frameLen := frameDataLen / m
			dataOffset := offset
			for i := range m {
				if dataOffset+frameLen > dataEnd {
					return 0, nil, errInvalidPacket
				}
				frames[i] = packet[dataOffset : dataOffset+frameLen]
				dataOffset += frameLen
			}
		}

		return toc, frames, nil
	default:
		return 0, nil, errInvalidPacket
	}
}

func parseFrameLength(data []byte, offset int) (int, int, error) {
	if offset >= len(data) {
		return 0, 0, errPacketTooShort
	}
	firstByte := int(data[offset])
	if firstByte < 252 {
		return firstByte, 1, nil
	}
	if offset+1 >= len(data) {
		return 0, 0, errPacketTooShort
	}
	secondByte := int(data[offset+1])
	return 4*secondByte + firstByte, 2, nil
}

//go:build gopus_dred || gopus_osce

package gopus

import internaldred "github.com/thesyncim/gopus/internal/dred"

// packetPaddingRegion mirrors the subset of opus_packet_parse_impl() that
// DRED payload discovery needs: the TOC-derived 48 kHz frame size, frame
// count, and the raw padding/extension region.
func packetPaddingRegion(packet []byte) (toc TOC, frameCount int, padding []byte, err error) {
	if len(packet) < 1 {
		return TOC{}, 0, nil, ErrPacketTooShort
	}

	toc = ParseTOC(packet[0])
	frameCode := toc.FrameCode
	offset := 1
	remaining := len(packet) - offset
	lastSize := remaining
	pad := 0

	switch frameCode {
	case 0:
		frameCount = 1
	case 1:
		frameCount = 2
		if remaining&1 != 0 {
			return TOC{}, 0, nil, ErrInvalidPacket
		}
		lastSize = remaining / 2
	case 2:
		frameCount = 2
		frame0Len, bytesRead, err := parseFrameLength(packet, offset)
		if err != nil {
			return TOC{}, 0, nil, err
		}
		remaining -= bytesRead
		if frame0Len < 0 || frame0Len > remaining {
			return TOC{}, 0, nil, ErrInvalidPacket
		}
		offset += bytesRead
		lastSize = remaining - frame0Len
	case 3:
		if remaining < 1 {
			return TOC{}, 0, nil, ErrInvalidPacket
		}
		ch := packet[offset]
		offset++
		remaining--
		frameCount = int(ch & 0x3F)
		if frameCount <= 0 || toc.FrameSize*frameCount > 5760 {
			return TOC{}, 0, nil, ErrInvalidPacket
		}
		if (ch & 0x40) != 0 {
			for {
				if remaining <= 0 {
					return TOC{}, 0, nil, ErrInvalidPacket
				}
				p := int(packet[offset])
				offset++
				remaining--

				tmp := p
				if p == 255 {
					tmp = 254
				}
				remaining -= tmp
				pad += tmp
				if remaining < 0 {
					return TOC{}, 0, nil, ErrInvalidPacket
				}
				if p != 255 {
					break
				}
			}
		}
		if (ch & 0x80) == 0 {
			lastSize = remaining / frameCount
			if lastSize*frameCount != remaining {
				return TOC{}, 0, nil, ErrInvalidPacket
			}
		} else {
			lastSize = remaining
			for i := 0; i < frameCount-1; i++ {
				frameLen, bytesRead, err := parseFrameLength(packet, offset)
				if err != nil {
					return TOC{}, 0, nil, err
				}
				remaining -= bytesRead
				if frameLen < 0 || frameLen > remaining {
					return TOC{}, 0, nil, ErrInvalidPacket
				}
				offset += bytesRead
				lastSize -= bytesRead + frameLen
				if lastSize < 0 {
					return TOC{}, 0, nil, ErrInvalidPacket
				}
			}
		}
	default:
		return TOC{}, 0, nil, ErrInvalidPacket
	}

	if lastSize > 1275 {
		return TOC{}, 0, nil, ErrInvalidPacket
	}

	paddingStart := offset + remaining
	if paddingStart < offset || paddingStart > len(packet) {
		return TOC{}, 0, nil, ErrInvalidPacket
	}
	if paddingStart+pad != len(packet) {
		return TOC{}, 0, nil, ErrInvalidPacket
	}
	if pad == 0 {
		return toc, frameCount, nil, nil
	}
	return toc, frameCount, packet[paddingStart:], nil
}

// findDREDPayload mirrors libopus dred_find_payload(): it scans packet
// extensions for the temporary DRED extension and returns the payload with the
// experimental prefix stripped. frameOffset is reported in 2.5 ms units.
func findDREDPayload(packet []byte) (payload []byte, frameOffset int, ok bool, err error) {
	toc, paddingFrameCount, padding, err := packetPaddingRegion(packet)
	if err != nil {
		return nil, 0, false, err
	}
	if len(padding) == 0 || paddingFrameCount <= 0 {
		return nil, 0, false, nil
	}

	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, padding, paddingFrameCount)

	for {
		var ext packetExtensionData
		ok, err = iter.next(&ext)
		if err != nil || !ok {
			return nil, 0, ok, err
		}
		if ext.ID != internaldred.ExtensionID {
			continue
		}
		if !internaldred.ValidExperimentalPayload(ext.Data) {
			continue
		}
		return ext.Data[internaldred.ExperimentalHeaderBytes:], ext.Frame * toc.FrameSize / 120, true, nil
	}
}

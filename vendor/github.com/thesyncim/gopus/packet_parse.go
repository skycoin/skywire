package gopus

import "errors"

const maxOpusFrameBytes = 1275

// Errors returned by packet parsing functions.
var (
	// ErrPacketTooShort indicates the packet is missing bytes required to parse
	// its TOC, frame-count, length, or padding fields.
	ErrPacketTooShort = errors.New("gopus: packet too short")
	// ErrInvalidFrameCount indicates a code 3 packet declared a frame count
	// outside the valid 1-48 range (RFC 6716 Section 3.2.5).
	ErrInvalidFrameCount = errors.New("gopus: invalid frame count (M > 48)")
	// ErrInvalidPacket indicates the packet violates the RFC 6716 framing rules,
	// for example inconsistent frame lengths or a total duration above 120 ms.
	ErrInvalidPacket = errors.New("gopus: invalid packet structure")
)

// PacketInfo contains parsed information about an Opus packet.
type PacketInfo struct {
	TOC        TOC   // Parsed TOC byte
	FrameCount int   // Number of frames (1-48 for code 3)
	FrameSizes []int // Size in bytes of each frame
	Padding    int   // Padding bytes (code 3 only)
	TotalSize  int   // Total packet size
}

// ParsePacket parses an Opus packet and returns information about its structure.
// It determines the frame boundaries based on the TOC byte's frame code (0-3).
func ParsePacket(data []byte) (PacketInfo, error) {
	if len(data) < 1 {
		return PacketInfo{}, ErrPacketTooShort
	}

	toc := ParseTOC(data[0])
	info := PacketInfo{
		TOC:       toc,
		TotalSize: len(data),
	}

	switch toc.FrameCode {
	case 0:
		// Code 0: One frame
		if len(data)-1 > maxOpusFrameBytes {
			return PacketInfo{}, ErrInvalidPacket
		}
		info.FrameCount = 1
		info.FrameSizes = []int{len(data) - 1}

	case 1:
		// Code 1: Two equal-sized frames
		frameDataLen := len(data) - 1
		if frameDataLen%2 != 0 {
			return PacketInfo{}, ErrInvalidPacket
		}
		frameSize := frameDataLen / 2
		if frameSize > maxOpusFrameBytes {
			return PacketInfo{}, ErrInvalidPacket
		}
		info.FrameCount = 2
		info.FrameSizes = []int{frameSize, frameSize}

	case 2:
		// Code 2: Two frames with different sizes
		if len(data) < 2 {
			return PacketInfo{}, ErrPacketTooShort
		}
		frame1Len, bytesRead, err := parseFrameLength(data, 1)
		if err != nil {
			return PacketInfo{}, err
		}
		headerLen := 1 + bytesRead
		frame2Len := len(data) - headerLen - frame1Len
		if frame2Len < 0 {
			return PacketInfo{}, ErrInvalidPacket
		}
		if frame2Len > maxOpusFrameBytes {
			return PacketInfo{}, ErrInvalidPacket
		}
		info.FrameCount = 2
		info.FrameSizes = []int{frame1Len, frame2Len}

	case 3:
		// Code 3: Arbitrary number of frames
		if len(data) < 2 {
			return PacketInfo{}, ErrPacketTooShort
		}
		frameCountByte := data[1]
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		m := int(frameCountByte & 0x3F)

		if m == 0 || m > 48 {
			return PacketInfo{}, ErrInvalidFrameCount
		}
		if toc.FrameSize*m > maxRepacketizerDuration48k {
			return PacketInfo{}, ErrInvalidPacket
		}

		offset := 2
		padding := 0

		// Parse padding if present
		if hasPadding {
			for {
				if offset >= len(data) {
					return PacketInfo{}, ErrPacketTooShort
				}
				padByte := int(data[offset])
				offset++
				if padByte == 255 {
					padding += 254
				} else {
					padding += padByte
				}
				if padByte < 255 {
					break
				}
			}
		}

		info.FrameCount = m
		info.Padding = padding
		info.FrameSizes = make([]int, m)

		if vbr {
			// VBR: Parse each frame length (except last)
			totalFrameLen := 0
			for i := 0; i < m-1; i++ {
				frameLen, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return PacketInfo{}, err
				}
				info.FrameSizes[i] = frameLen
				totalFrameLen += frameLen
				offset += bytesRead
			}
			// Last frame is remainder
			lastFrameLen := len(data) - offset - padding - totalFrameLen
			if lastFrameLen < 0 {
				return PacketInfo{}, ErrInvalidPacket
			}
			if lastFrameLen > maxOpusFrameBytes {
				return PacketInfo{}, ErrInvalidPacket
			}
			info.FrameSizes[m-1] = lastFrameLen
		} else {
			// CBR: Parse single frame length, all frames are same size
			// For CBR, no frame lengths are encoded. All frames share the
			// remaining bytes (minus padding) equally.
			frameDataLen := len(data) - offset - padding
			if frameDataLen < 0 {
				return PacketInfo{}, ErrInvalidPacket
			}
			if m == 0 {
				return PacketInfo{}, ErrInvalidFrameCount
			}
			if frameDataLen%m != 0 {
				return PacketInfo{}, ErrInvalidPacket
			}
			frameLen := frameDataLen / m
			if frameLen > maxOpusFrameBytes {
				return PacketInfo{}, ErrInvalidPacket
			}
			for i := range m {
				info.FrameSizes[i] = frameLen
			}
		}
	}

	return info, nil
}

// parseFrameLength parses a frame length from the packet data at the given offset.
// Per RFC 6716 Section 3.2.1, lengths < 252 use one byte, lengths >= 252 use two bytes.
// Returns the length, number of bytes read, and any error.
func parseFrameLength(data []byte, offset int) (int, int, error) {
	if offset >= len(data) {
		return 0, 0, ErrPacketTooShort
	}

	firstByte := int(data[offset])
	if firstByte < 252 {
		return firstByte, 1, nil
	}

	// Two-byte encoding: length = 4*secondByte + firstByte
	if offset+1 >= len(data) {
		return 0, 0, ErrPacketTooShort
	}
	secondByte := int(data[offset+1])
	return 4*secondByte + firstByte, 2, nil
}

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

// PacketInfo describes an Opus packet's TOC and frame layout.
type PacketInfo struct {
	TOC        TOC   // Parsed TOC byte.
	FrameCount int   // Number of frames in the packet.
	FrameSizes []int // Frame payload sizes in bytes, excluding headers and padding.
	Padding    int   // Trailing padding bytes; nonzero only for code 3 packets.
	TotalSize  int   // Complete packet size in bytes, including headers and padding.
}

// ParsePacket parses a complete Opus packet and returns its frame layout.
// FrameSizes contains payload lengths in packet order; packet headers and
// trailing padding are reported separately. It returns an error for truncated,
// malformed, overlong, or over-duration packets.
func ParsePacket(data []byte) (PacketInfo, error) {
	return parsePacketInto(data, nil)
}

// parsePacketInto shares framing validation between ParsePacket and the
// repacketizer. A supplied scratch array receives frame lengths; with no
// scratch, it allocates FrameSizes at the same validated points as ParsePacket.
func parsePacketInto(data []byte, scratch *[maxRepacketizerFrames]int) (PacketInfo, error) {
	if len(data) < 1 {
		return PacketInfo{}, ErrPacketTooShort
	}

	toc := ParseTOC(data[0])
	info := PacketInfo{TOC: toc, TotalSize: len(data)}

	switch toc.FrameCode {
	case 0:
		// Code 0: One frame
		if len(data)-1 > maxOpusFrameBytes {
			return PacketInfo{}, ErrInvalidPacket
		}
		info.FrameCount = 1
		info.FrameSizes = packetFrameSizes(scratch, info.FrameCount)
		info.FrameSizes[0] = len(data) - 1

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
		info.FrameSizes = packetFrameSizes(scratch, info.FrameCount)
		info.FrameSizes[0], info.FrameSizes[1] = frameSize, frameSize

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
		info.FrameSizes = packetFrameSizes(scratch, info.FrameCount)
		info.FrameSizes[0], info.FrameSizes[1] = frame1Len, frame2Len

	case 3:
		// Code 3: Arbitrary number of frames
		if len(data) < 2 {
			return PacketInfo{}, ErrPacketTooShort
		}
		frameCountByte := data[1]
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		frameCount := int(frameCountByte & 0x3F)

		if frameCount == 0 || frameCount > maxRepacketizerFrames {
			return PacketInfo{}, ErrInvalidFrameCount
		}
		if toc.FrameSize*frameCount > maxRepacketizerDuration48k {
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
		info.FrameCount = frameCount
		info.FrameSizes = packetFrameSizes(scratch, frameCount)
		info.Padding = padding

		if vbr {
			// VBR: Parse each frame length (except last)
			totalFrameLen := 0
			for i := 0; i < frameCount-1; i++ {
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
			info.FrameSizes[frameCount-1] = lastFrameLen
		} else {
			// CBR: Parse single frame length, all frames are same size
			// For CBR, no frame lengths are encoded. All frames share the
			// remaining bytes (minus padding) equally.
			frameDataLen := len(data) - offset - padding
			if frameDataLen < 0 {
				return PacketInfo{}, ErrInvalidPacket
			}
			if frameDataLen%frameCount != 0 {
				return PacketInfo{}, ErrInvalidPacket
			}
			frameLen := frameDataLen / frameCount
			if frameLen > maxOpusFrameBytes {
				return PacketInfo{}, ErrInvalidPacket
			}
			for i := range frameCount {
				info.FrameSizes[i] = frameLen
			}
		}
	}

	return info, nil
}

func packetFrameSizes(scratch *[maxRepacketizerFrames]int, frameCount int) []int {
	if scratch == nil {
		return make([]int, frameCount)
	}
	return scratch[:frameCount]
}

// validatePacketFraming checks the same packet-size constraints as ParsePacket
// without materializing a FrameSizes slice. DecodeWithFEC only needs to reject
// malformed framing before it changes decoder state.
func validatePacketFraming(data []byte) error {
	toc, count, err := packetFrameCount(data)
	if err != nil {
		return err
	}
	switch toc.FrameCode {
	case 0:
		if len(data)-1 > maxOpusFrameBytes {
			return ErrInvalidPacket
		}
	case 1:
		frameDataLen := len(data) - 1
		if frameDataLen%2 != 0 || frameDataLen/2 > maxOpusFrameBytes {
			return ErrInvalidPacket
		}
	case 2:
		if len(data) < 2 {
			return ErrPacketTooShort
		}
		first, headerBytes, err := parseFrameLength(data, 1)
		if err != nil {
			return err
		}
		last := len(data) - 1 - headerBytes - first
		if last < 0 || last > maxOpusFrameBytes {
			return ErrInvalidPacket
		}
	case 3:
		vbr := data[1]&0x80 != 0
		hasPadding := data[1]&0x40 != 0
		offset := 2
		padding := 0
		if hasPadding {
			for {
				if offset >= len(data) {
					return ErrPacketTooShort
				}
				b := int(data[offset])
				offset++
				if b == 255 {
					padding += 254
				} else {
					padding += b
					break
				}
			}
		}
		if vbr {
			total := 0
			for range count - 1 {
				n, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return err
				}
				total += n
				offset += bytesRead
			}
			last := len(data) - offset - padding - total
			if last < 0 || last > maxOpusFrameBytes {
				return ErrInvalidPacket
			}
		} else {
			frameDataLen := len(data) - offset - padding
			if frameDataLen < 0 || frameDataLen%count != 0 || frameDataLen/count > maxOpusFrameBytes {
				return ErrInvalidPacket
			}
		}
	}
	return nil
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

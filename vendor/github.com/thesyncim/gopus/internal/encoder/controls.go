// This file provides bitrate and bandwidth control for the Opus encoder:
// VBR/CBR/CVBR mode selection and bitrate management per RFC 6716 Section 2.1.1,
// mirroring the rate-control helpers in libopus src/opus_encoder.c.

package encoder

// BitrateMode specifies how the encoder manages packet sizes.
type BitrateMode int

const (
	// ModeVBR is variable bitrate mode (default).
	// Packet size varies based on content complexity.
	// Provides best quality for a given average bitrate.
	ModeVBR BitrateMode = iota

	// ModeCVBR is constrained variable bitrate mode.
	// Packet size varies but stays within +/-15% of target.
	// Good balance of quality and bandwidth predictability.
	ModeCVBR

	// ModeCBR is constant bitrate mode.
	// Every packet is exactly the same size (or within 1 byte).
	// Required for some streaming protocols.
	ModeCBR
)

// Bitrate limits per RFC 6716
const (
	BitrateAuto = -1000
	BitrateMax  = -1

	MinBitrate = 500    // libopus OPUS_SET_BITRATE minimum
	MaxBitrate = 750000 // libopus OPUS_SET_BITRATE per-channel maximum

	// Mode-specific typical ranges
	SILKMinBitrate   = 6000   // 6 kbps
	SILKMaxBitrate   = 40000  // 40 kbps (WB)
	CELTMinBitrate   = 32000  // 32 kbps
	CELTMaxBitrate   = 510000 // 510 kbps
	HybridMinBitrate = 12000  // 12 kbps
	HybridMaxBitrate = 128000 // 128 kbps typical

	// Maximum SILK packet size in bytes (libopus MAX_DATA_BYTES).
	maxSilkPacketBytes = 1275

	// libopusMaxDataBytesCap mirrors opus_encoder.c opus_encode_native():
	//   max_data_bytes = IMIN(orig_max_data_bytes, 1276)
	// The SILK maxBits budget for the primary frame is (max_data_bytes-1)*8 and the
	// CELT nb_compr_bytes budget is (max_data_bytes-1)-redundancy_bytes, so the
	// VBR/CVBR rate-control loops see a 1276-byte cap rather than the 1275-byte
	// RFC Opus packet limit.
	libopusMaxDataBytesCap = 1276
)

// CVBR tolerance (percentage)
const CVBRTolerance float32 = 0.15 // +/- 15%

// ValidBitrate returns true if the bitrate is within Opus limits.
func ValidBitrate(bitrate int) bool {
	return bitrate == BitrateAuto || bitrate == BitrateMax || (bitrate >= MinBitrate && bitrate <= MaxBitrate)
}

// ClampBitrate ensures bitrate is within valid range.
func ClampBitrate(bitrate int) int {
	return clampBitrateForChannels(bitrate, 1)
}

func clampBitrateForChannels(bitrate, channels int) int {
	if bitrate == BitrateAuto || bitrate == BitrateMax {
		return bitrate
	}
	if channels < 1 {
		channels = 1
	}
	if bitrate < MinBitrate {
		return MinBitrate
	}
	maxBitrate := MaxBitrate * channels
	if bitrate > maxBitrate {
		return maxBitrate
	}
	return bitrate
}

func clampAllocatedBitrate(bitrate, channels int) int {
	return clampBitrateForChannels(bitrate, channels)
}

// targetBytesForBitrate computes target packet size in bytes.
func (e *Encoder) targetBytesForBitrate(bitrate, frameSize int) int {
	unitsPerFrame := 6 * int(e.sampleRate) / frameSize
	if unitsPerFrame <= 0 {
		return 0
	}
	bits := bitrate * 6 / unitsPerFrame
	return (bits + 4) / 8
}

func resolveUserBitrate(userBitrate, sampleRate, channels, frameSize, maxDataBytes int) int {
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if channels <= 0 {
		channels = 1
	}
	if frameSize <= 0 {
		frameSize = sampleRate / 400
	}
	maxBitrate := maxDataBytes * 8 * sampleRate / frameSize
	var user int
	switch userBitrate {
	case BitrateAuto:
		user = 60*sampleRate/frameSize + sampleRate*channels
	case BitrateMax:
		user = 1500000
	default:
		user = clampBitrateForChannels(userBitrate, channels)
	}
	if user > maxBitrate {
		return maxBitrate
	}
	return user
}

// silkPayloadMaxBits mirrors libopus SILK maxBits budgeting:
// the Opus TOC byte is not part of the SILK range payload budget.
func silkPayloadMaxBits(maxPacketBytes int) int {
	if maxPacketBytes <= 1 {
		return 0
	}
	return (maxPacketBytes - 1) * 8
}

// padToSize pads packet to exact size without truncating.
// Used for CBR mode.
func padToSize(packet []byte, targetSize int) []byte {
	padded := make([]byte, targetSize)
	return padToSizeInto(padded, packet, targetSize)
}

// padToSizeInto pads packet to exact size in dst without heap allocation.
// dst and packet may share the same backing array.
func padToSizeInto(dst, packet []byte, targetSize int) []byte {
	if len(packet) >= targetSize {
		return packet
	}
	if len(packet) == 0 || len(dst) < targetSize {
		return packet
	}

	var frameStarts [48]int
	var frameLens [48]int
	toc, frameCount, vbr, totalFrameBytes, err := packetFrameLayout(packet, &frameStarts, &frameLens)
	if err != nil || frameCount == 0 || frameCount > 48 {
		return packet
	}

	lengthBytes := 0
	if vbr {
		for i := 0; i < frameCount-1; i++ {
			lengthBytes += frameLengthBytes(frameLens[i])
		}
	}

	// libopus opus_packet_pad() restarts from code-3 framing when any growth is
	// requested, even if no explicit padding payload is needed.
	base := 2 + lengthBytes + totalFrameBytes
	if base > targetSize {
		return packet
	}

	padAmount := targetSize - base
	paddingBytes := 0
	if padAmount > 0 {
		paddingBytes = paddingLengthBytes(padAmount)
	}
	payloadOffset := 2 + paddingBytes + lengthBytes

	dst = dst[:targetSize]
	writeOffset := payloadOffset + totalFrameBytes
	for i := frameCount - 1; i >= 0; i-- {
		writeOffset -= frameLens[i]
		copy(dst[writeOffset:writeOffset+frameLens[i]], packet[frameStarts[i]:frameStarts[i]+frameLens[i]])
	}

	dst[0] = (toc & 0xFC) | 0x03
	countByte := byte(frameCount & 0x3F)
	if vbr {
		countByte |= 0x80
	}
	if padAmount > 0 {
		countByte |= 0x40
	}
	dst[1] = countByte

	offset := 2
	if padAmount > 0 {
		offset += writePaddingLength(dst[offset:], padAmount)
	}
	if vbr {
		for i := 0; i < frameCount-1; i++ {
			offset += writeFrameLength(dst[offset:], frameLens[i])
		}
	}
	offset += totalFrameBytes
	if offset > len(dst) {
		return packet
	}
	clear(dst[offset:])

	return dst
}

func packetFrameLayout(packet []byte, starts *[48]int, lens *[48]int) (toc byte, frameCount int, vbr bool, totalBytes int, err error) {
	if len(packet) < 1 {
		err = errPacketTooShort
		return
	}

	toc = packet[0]
	switch toc & 0x03 {
	case 0:
		starts[0] = 1
		lens[0] = len(packet) - 1
		return toc, 1, false, lens[0], nil
	case 1:
		frameDataLen := len(packet) - 1
		if frameDataLen < 0 || frameDataLen%2 != 0 {
			err = errInvalidPacket
			return
		}
		frameLen := frameDataLen / 2
		starts[0], lens[0] = 1, frameLen
		starts[1], lens[1] = 1+frameLen, frameLen
		return toc, 2, false, frameDataLen, nil
	case 2:
		if len(packet) < 2 {
			err = errPacketTooShort
			return
		}
		frame1Len, bytesRead, parseErr := parseFrameLength(packet, 1)
		if parseErr != nil {
			err = parseErr
			return
		}
		headerLen := 1 + bytesRead
		if headerLen+frame1Len > len(packet) {
			err = errInvalidPacket
			return
		}
		frame2Len := len(packet) - headerLen - frame1Len
		if frame2Len < 0 {
			err = errInvalidPacket
			return
		}
		starts[0], lens[0] = headerLen, frame1Len
		starts[1], lens[1] = headerLen+frame1Len, frame2Len
		return toc, 2, frame1Len != frame2Len, frame1Len + frame2Len, nil
	case 3:
		if len(packet) < 2 {
			err = errPacketTooShort
			return
		}
		countByte := packet[1]
		vbr = (countByte & 0x80) != 0
		hasPadding := (countByte & 0x40) != 0
		frameCount = int(countByte & 0x3F)
		if frameCount == 0 || frameCount > 48 {
			err = ErrInvalidFrameCount
			return
		}
		offset := 2
		padding := 0
		if hasPadding {
			for {
				if offset >= len(packet) {
					err = errPacketTooShort
					return
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
			err = errInvalidPacket
			return
		}
		if vbr {
			for i := 0; i < frameCount-1; i++ {
				frameLen, bytesRead, parseErr := parseFrameLength(packet, offset)
				if parseErr != nil {
					err = parseErr
					return
				}
				offset += bytesRead
				lens[i] = frameLen
			}
			dataOffset := offset
			for i := 0; i < frameCount-1; i++ {
				if dataOffset+lens[i] > dataEnd {
					err = errInvalidPacket
					return
				}
				starts[i] = dataOffset
				dataOffset += lens[i]
				totalBytes += lens[i]
			}
			lastLen := dataEnd - dataOffset
			if lastLen < 0 {
				err = errInvalidPacket
				return
			}
			starts[frameCount-1], lens[frameCount-1] = dataOffset, lastLen
			totalBytes += lastLen
			return toc, frameCount, true, totalBytes, nil
		}
		frameDataLen := dataEnd - offset
		if frameDataLen < 0 || frameDataLen%frameCount != 0 {
			err = errInvalidPacket
			return
		}
		frameLen := frameDataLen / frameCount
		dataOffset := offset
		for i := 0; i < frameCount; i++ {
			starts[i], lens[i] = dataOffset, frameLen
			dataOffset += frameLen
		}
		return toc, frameCount, false, frameDataLen, nil
	default:
		err = errInvalidPacket
		return
	}
}

// constrainSize adjusts packet size to stay within CVBR tolerance.
func constrainSize(packet []byte, target int, tolerance float32) []byte {
	maxSize := int(float32(target) * (1 + tolerance))

	if len(packet) > maxSize {
		// Upper-bound enforcement requires re-encoding with a tighter budget.
		// Keep the packet unchanged here to avoid altering framing semantics.
		return packet
	}
	return packet
}

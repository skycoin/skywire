package multistream

import "errors"

// Errors for multistream packet parsing.
var (
	// ErrPacketTooShort indicates insufficient data in the multistream packet.
	ErrPacketTooShort = errors.New("multistream: packet too short")

	// ErrInvalidPacket indicates malformed Opus packet framing.
	ErrInvalidPacket = errors.New("multistream: invalid packet framing")

	// ErrDurationMismatch indicates streams have different frame durations.
	ErrDurationMismatch = errors.New("multistream: streams have different frame durations")

	// ErrInvalidStreamCount indicates an invalid stream count (must be >= 1).
	ErrInvalidStreamCount = errors.New("multistream: invalid stream count (must be >= 1)")
)

// parseSelfDelimitedLength parses a self-delimiting packet length from the data.
// Per RFC 6716 Section 3.2.1, lengths use a 1-2 byte encoding:
//   - If first byte < 252: length = firstByte (1 byte consumed)
//   - Otherwise: length = 4*secondByte + firstByte (2 bytes consumed)
//
// This is the same encoding used for frame lengths in standard Opus packets.
//
// Returns:
//   - length: the decoded packet length in bytes
//   - consumed: number of bytes consumed for the length field (1 or 2)
//   - err: ErrPacketTooShort if insufficient bytes available
func parseSelfDelimitedLength(data []byte) (length, consumed int, err error) {
	if len(data) == 0 {
		return 0, 0, ErrPacketTooShort
	}

	firstByte := int(data[0])
	if firstByte < 252 {
		return firstByte, 1, nil
	}

	// Two-byte encoding
	if len(data) < 2 {
		return 0, 0, ErrPacketTooShort
	}
	secondByte := int(data[1])
	return 4*secondByte + firstByte, 2, nil
}

// parseMultistreamPacket extracts individual stream packets from a multistream packet.
// Per RFC 6716 Appendix B:
//   - First N-1 streams use self-delimited Opus packet framing
//   - Last stream uses standard framing (consumes remaining bytes)
//
// Parameters:
//   - data: raw multistream packet data
//   - numStreams: total number of streams (N)
//
// Returns:
//   - packets: slice of N standard-framed Opus packets, one per stream
//   - err: parsing error if data is malformed
func parseMultistreamPacket(data []byte, numStreams int) ([][]byte, error) {
	return parseMultistreamPacketInto(nil, data, numStreams)
}

// parseMultistreamPacketInto is parseMultistreamPacket with a caller-provided
// slice header reused across calls. The returned sub-slices alias data and the
// internal self-delimited reframing buffers; callers must not retain them past
// the next decode call.
func parseMultistreamPacketInto(scratch [][]byte, data []byte, numStreams int) ([][]byte, error) {
	return parseMultistreamPacketScratch(scratch, nil, nil, data, numStreams)
}

// parseMultistreamPacketScratch is parseMultistreamPacketInto with additional
// reusable parser scratch and a reframe arena. The first N-1 streams are
// self-delimited and must be reframed to standard form; the resulting bytes are
// carved as non-overlapping slices of arena (sized once to len(data)) so all of
// them coexist for the per-stream decode loop that follows. parser/arena may be
// nil to fall back to per-packet allocation.
func parseMultistreamPacketScratch(scratch [][]byte, parser *packetScratch, arena *[]byte, data []byte, numStreams int) ([][]byte, error) {
	if numStreams < 1 {
		return nil, ErrInvalidStreamCount
	}

	packets := scratch
	if cap(packets) < numStreams {
		packets = make([][]byte, numStreams)
	}
	packets = packets[:numStreams]
	offset := 0

	var buf []byte
	if arena != nil && numStreams > 1 {
		// A reframed standard packet is never larger than the self-delimited
		// bytes it consumes, so the whole arena is bounded by len(data).
		if cap(*arena) < len(data) {
			*arena = make([]byte, len(data))
		}
		buf = (*arena)[:len(data)]
	}
	arenaOff := 0

	// Parse first N-1 packets with self-delimited framing and convert them
	// back to standard framing for the elementary decoders.
	for i := 0; i < numStreams-1; i++ {
		if offset >= len(data) {
			return nil, ErrPacketTooShort
		}

		if buf != nil {
			written, consumed, err := decodeSelfDelimitedPacketInto(parser, buf[arenaOff:], data[offset:])
			if err != nil {
				return nil, err
			}
			packets[i] = buf[arenaOff : arenaOff+written]
			arenaOff += written
			offset += consumed
			continue
		}

		packet, consumed, err := decodeSelfDelimitedPacket(data[offset:])
		if err != nil {
			return nil, err
		}
		packets[i] = packet
		offset += consumed
	}

	// Last packet uses remaining bytes (standard framing)
	if offset >= len(data) {
		return nil, ErrPacketTooShort
	}
	lastPacket := data[offset:]
	if _, err := parseOpusPacketInto(parser, lastPacket, false); err != nil {
		return nil, err
	}
	packets[numStreams-1] = lastPacket

	return packets, nil
}

// getFrameDuration returns the frame duration in samples at 48kHz from a packet's TOC byte.
// This is used to verify all streams in a multistream packet have consistent timing.
//
// Parameters:
//   - packet: raw Opus packet data (first byte is TOC)
//
// Returns:
//   - Frame size in samples at 48kHz (120, 240, 480, 960, 1920, or 2880)
//   - Returns 0 if packet is empty
//
// Reference: RFC 6716 Section 3.1
func getFrameDuration(packet []byte) int {
	if len(packet) == 0 {
		return 0
	}

	parsed, err := parseOpusPacket(packet, false)
	if err != nil || len(parsed.frames) == 0 {
		return 0
	}

	return opusSamplesPerFrame48k(packet[0]) * len(parsed.frames)
}

func getFrameDurationAtRateScratch(parser *packetScratch, packet []byte, sampleRate int) int {
	if len(packet) == 0 {
		return 0
	}

	parsed, err := parseOpusPacketInto(parser, packet, false)
	if err != nil || len(parsed.frames) == 0 {
		return 0
	}

	return opusSamplesPerFrameAtRate(packet[0], sampleRate) * len(parsed.frames)
}

func opusSamplesPerFrame48k(toc byte) int {
	// TOC byte structure: config (5 bits) | stereo (1 bit) | code (2 bits).
	config := toc >> 3

	// Frame size table indexed by config (0-31). Matches gopus.configTable
	// from packet.go and libopus opus_packet_get_samples_per_frame(..., 48000).
	frameSizeTable := [32]int{
		480, 960, 1920, 2880,
		480, 960, 1920, 2880,
		480, 960, 1920, 2880,
		480, 960,
		480, 960,
		120, 240, 480, 960,
		120, 240, 480, 960,
		120, 240, 480, 960,
		120, 240, 480, 960,
	}
	return frameSizeTable[config]
}

func opusSamplesPerFrameAtRate(toc byte, sampleRate int) int {
	if toc&0x80 != 0 {
		audioSize := int((toc >> 3) & 0x3)
		return (sampleRate << audioSize) / 400
	}
	if toc&0x60 == 0x60 {
		if toc&0x08 != 0 {
			return sampleRate / 50
		}
		return sampleRate / 100
	}
	audioSize := int((toc >> 3) & 0x3)
	if audioSize == 3 {
		return sampleRate * 60 / 1000
	}
	return (sampleRate << audioSize) / 100
}

// validateStreamDurations checks that all stream packets have the same frame duration.
// Returns the common duration if valid, or an error if durations don't match.
func validateStreamDurations(packets [][]byte) (int, error) {
	if len(packets) == 0 {
		return 0, ErrInvalidStreamCount
	}

	duration := getFrameDuration(packets[0])
	if duration == 0 {
		return 0, ErrPacketTooShort
	}

	for i := 1; i < len(packets); i++ {
		streamDuration := getFrameDuration(packets[i])
		if streamDuration != duration {
			return 0, ErrDurationMismatch
		}
	}

	return duration, nil
}

func validateStreamDurationsAtRate(packets [][]byte, sampleRate int) (int, error) {
	return validateStreamDurationsAtRateScratch(nil, packets, sampleRate)
}

func validateStreamDurationsAtRateScratch(parser *packetScratch, packets [][]byte, sampleRate int) (int, error) {
	if len(packets) == 0 {
		return 0, ErrInvalidStreamCount
	}

	duration := getFrameDurationAtRateScratch(parser, packets[0], sampleRate)
	if duration == 0 {
		return 0, ErrPacketTooShort
	}

	for i := 1; i < len(packets); i++ {
		streamDuration := getFrameDurationAtRateScratch(parser, packets[i], sampleRate)
		if streamDuration != duration {
			return 0, ErrDurationMismatch
		}
	}
	if duration*25 > sampleRate*3 {
		return 0, ErrInvalidPacket
	}

	return duration, nil
}

// PacketDuration returns the common packet duration for a multistream packet in
// 48 kHz samples per channel.
func PacketDuration(data []byte, numStreams int) (int, error) {
	packets, err := parseMultistreamPacket(data, numStreams)
	if err != nil {
		return 0, err
	}
	return validateStreamDurations(packets)
}

// PacketDurationAtRate returns the common packet duration in samples per
// channel at sampleRate, matching opus_packet_get_nb_samples().
func PacketDurationAtRate(data []byte, numStreams, sampleRate int) (int, error) {
	packets, err := parseMultistreamPacket(data, numStreams)
	if err != nil {
		return 0, err
	}
	return validateStreamDurationsAtRate(packets, sampleRate)
}

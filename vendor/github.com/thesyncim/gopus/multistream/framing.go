package multistream

const (
	maxOpusFrameBytes       = 1275
	maxOpusPacketDuration48 = 5760

	// msFrameTmp mirrors libopus opus_multistream_encoder.c MS_FRAME_TMP
	// (#define MS_FRAME_TMP (6*1275+12)): the size of the per-stream scratch
	// buffer and the upper clamp applied to each stream's curr_max budget.
	msFrameTmp = 6*1275 + 12
)

type parsedOpusPacket struct {
	tocBase           byte
	frames            [][]byte
	padding           []byte
	paddingFrameCount int
	consumed          int
}

// packetScratch holds reusable parse/build working buffers so the multistream
// hot paths can split and reframe packets without per-call allocation. The
// returned parsedOpusPacket.frames aliases frames here, so a parse result is
// only valid until the next parseInto on the same scratch; the hot-path callers
// parse-then-build atomically before reusing it.
type packetScratch struct {
	frameSizes []int
	frames     [][]byte
	lengths    []int // build-side frame length scratch
}

func (p *packetScratch) sizes(n int) []int {
	if cap(p.frameSizes) < n {
		p.frameSizes = make([]int, n)
	}
	return p.frameSizes[:n]
}

func (p *packetScratch) frameSlots(n int) [][]byte {
	if cap(p.frames) < n {
		p.frames = make([][]byte, n)
	}
	return p.frames[:n]
}

func (p *packetScratch) lengthSlots(n int) []int {
	if cap(p.lengths) < n {
		p.lengths = make([]int, n)
	}
	return p.lengths[:n]
}

// parseOpusPacket parses one Opus packet from data.
// If selfDelimited is true, data may contain trailing bytes for subsequent
// packets and this function consumes exactly one self-delimited packet.
func parseOpusPacket(data []byte, selfDelimited bool) (parsedOpusPacket, error) {
	return parseOpusPacketInto(nil, data, selfDelimited)
}

// parseOpusPacketInto is parseOpusPacket with caller-provided reusable scratch.
// When scratch is nil it allocates fresh working buffers (the legacy behavior).
func parseOpusPacketInto(scratch *packetScratch, data []byte, selfDelimited bool) (parsedOpusPacket, error) {
	if len(data) < 1 {
		if selfDelimited {
			return parsedOpusPacket{}, ErrInvalidPacket
		}
		return parsedOpusPacket{}, ErrPacketTooShort
	}

	toc := data[0]
	code := toc & 0x03
	offset := 1
	padding := 0
	frameCount := 1
	var frameSizes []int
	if scratch != nil {
		frameSizes = scratch.sizes(2)[:0]
	} else {
		frameSizes = make([]int, 0, 2)
	}

	switch code {
	case 0:
		frameCount = 1
		if selfDelimited {
			length, consumed, err := parseSelfDelimitedLength(data[offset:])
			if err != nil {
				return parsedOpusPacket{}, ErrInvalidPacket
			}
			offset += consumed
			frameSizes = append(frameSizes, length)
		} else {
			frameLen := len(data) - offset
			if frameLen > maxOpusFrameBytes {
				return parsedOpusPacket{}, ErrInvalidPacket
			}
			frameSizes = append(frameSizes, frameLen)
		}

	case 1:
		frameCount = 2
		if selfDelimited {
			length, consumed, err := parseSelfDelimitedLength(data[offset:])
			if err != nil {
				return parsedOpusPacket{}, ErrInvalidPacket
			}
			offset += consumed
			frameSizes = append(frameSizes, length, length)
		} else {
			frameDataLen := len(data) - offset
			if frameDataLen < 0 || frameDataLen%2 != 0 {
				return parsedOpusPacket{}, ErrInvalidPacket
			}
			frameLen := frameDataLen / 2
			if frameLen > maxOpusFrameBytes {
				return parsedOpusPacket{}, ErrInvalidPacket
			}
			frameSizes = append(frameSizes, frameLen, frameLen)
		}

	case 2:
		length0, consumed, err := parseSelfDelimitedLength(data[offset:])
		if err != nil {
			if selfDelimited {
				return parsedOpusPacket{}, ErrInvalidPacket
			}
			return parsedOpusPacket{}, err
		}
		offset += consumed

		var length1 int
		if selfDelimited {
			length1, consumed, err = parseSelfDelimitedLength(data[offset:])
			if err != nil {
				return parsedOpusPacket{}, ErrInvalidPacket
			}
			offset += consumed
		} else {
			length1 = len(data) - offset - length0
		}

		if length0 < 0 || length1 < 0 {
			return parsedOpusPacket{}, ErrInvalidPacket
		}
		if !selfDelimited && length1 > maxOpusFrameBytes {
			return parsedOpusPacket{}, ErrInvalidPacket
		}
		frameCount = 2
		frameSizes = append(frameSizes, length0, length1)

	case 3:
		if offset >= len(data) {
			if selfDelimited {
				return parsedOpusPacket{}, ErrInvalidPacket
			}
			return parsedOpusPacket{}, ErrPacketTooShort
		}

		frameCountByte := data[offset]
		offset++
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		frameCount = int(frameCountByte & 0x3F)
		if frameCount == 0 || frameCount > 48 {
			return parsedOpusPacket{}, ErrInvalidPacket
		}
		if opusSamplesPerFrame48k(toc)*frameCount > maxOpusPacketDuration48 {
			return parsedOpusPacket{}, ErrInvalidPacket
		}

		if scratch != nil {
			frameSizes = scratch.sizes(frameCount)
		} else {
			frameSizes = make([]int, frameCount)
		}
		if hasPadding {
			for {
				if offset >= len(data) {
					if selfDelimited {
						return parsedOpusPacket{}, ErrInvalidPacket
					}
					return parsedOpusPacket{}, ErrPacketTooShort
				}
				padByte := int(data[offset])
				offset++
				if padByte == 255 {
					padding += 254
				} else {
					padding += padByte
					break
				}
			}
		}

		if vbr {
			totalKnown := 0
			for i := 0; i < frameCount-1; i++ {
				frameLen, consumed, err := parseSelfDelimitedLength(data[offset:])
				if err != nil {
					if selfDelimited {
						return parsedOpusPacket{}, ErrInvalidPacket
					}
					return parsedOpusPacket{}, err
				}
				offset += consumed
				frameSizes[i] = frameLen
				totalKnown += frameLen
			}

			if selfDelimited {
				lastLen, consumed, err := parseSelfDelimitedLength(data[offset:])
				if err != nil {
					return parsedOpusPacket{}, ErrInvalidPacket
				}
				offset += consumed
				frameSizes[frameCount-1] = lastLen
			} else {
				lastLen := len(data) - offset - padding - totalKnown
				if lastLen < 0 {
					return parsedOpusPacket{}, ErrInvalidPacket
				}
				if lastLen > maxOpusFrameBytes {
					return parsedOpusPacket{}, ErrInvalidPacket
				}
				frameSizes[frameCount-1] = lastLen
			}
		} else {
			if selfDelimited {
				frameLen, consumed, err := parseSelfDelimitedLength(data[offset:])
				if err != nil {
					return parsedOpusPacket{}, ErrInvalidPacket
				}
				offset += consumed
				for i := 0; i < frameCount; i++ {
					frameSizes[i] = frameLen
				}
			} else {
				frameDataLen := len(data) - offset - padding
				if frameDataLen < 0 || frameDataLen%frameCount != 0 {
					return parsedOpusPacket{}, ErrInvalidPacket
				}
				frameLen := frameDataLen / frameCount
				if frameLen > maxOpusFrameBytes {
					return parsedOpusPacket{}, ErrInvalidPacket
				}
				for i := 0; i < frameCount; i++ {
					frameSizes[i] = frameLen
				}
			}
		}

	default:
		return parsedOpusPacket{}, ErrInvalidPacket
	}

	frameBytes := 0
	for _, sz := range frameSizes {
		if sz < 0 {
			return parsedOpusPacket{}, ErrInvalidPacket
		}
		frameBytes += sz
	}

	consumed := offset + frameBytes + padding
	if consumed > len(data) {
		if selfDelimited {
			return parsedOpusPacket{}, ErrInvalidPacket
		}
		return parsedOpusPacket{}, ErrPacketTooShort
	}
	if !selfDelimited && consumed != len(data) {
		return parsedOpusPacket{}, ErrInvalidPacket
	}

	var frames [][]byte
	if scratch != nil {
		frames = scratch.frameSlots(frameCount)
	} else {
		frames = make([][]byte, frameCount)
	}
	frameOffset := offset
	frameEnd := offset + frameBytes
	for i := 0; i < frameCount; i++ {
		next := frameOffset + frameSizes[i]
		if next > frameEnd {
			return parsedOpusPacket{}, ErrInvalidPacket
		}
		frames[i] = data[frameOffset:next]
		frameOffset = next
	}

	var rawPadding []byte
	if padding > 0 {
		rawPadding = data[frameEnd:consumed]
	}

	return parsedOpusPacket{
		tocBase:           toc & 0xFC,
		frames:            frames,
		padding:           rawPadding,
		paddingFrameCount: frameCount,
		consumed:          consumed,
	}, nil
}

func frameLengthBytes(length int) int {
	if length < 252 {
		return 1
	}
	return 2
}

func writeFrameLength(dst []byte, length int) int {
	if length < 252 {
		dst[0] = byte(length)
		return 1
	}
	firstByte := 252 + (length % 4)
	secondByte := (length - firstByte) / 4
	dst[0] = byte(firstByte)
	dst[1] = byte(secondByte)
	return 2
}

func paddingLengthBytes(length int) int {
	bytes := 1
	for length > 254 {
		bytes++
		length -= 254
	}
	return bytes
}

func writePaddingLength(dst []byte, length int) int {
	offset := 0
	for length > 254 {
		dst[offset] = 255
		offset++
		length -= 254
	}
	dst[offset] = byte(length)
	return offset + 1
}

// buildOpusPacketFromFrames assembles an Opus packet from frames.
// When selfDelimited is true, it writes RFC 6716 Appendix B self-delimited framing.
func buildOpusPacketFromFrames(tocBase byte, frames [][]byte, selfDelimited bool, dst []byte) (int, error) {
	return buildOpusPacketFromFramesInto(nil, tocBase, frames, selfDelimited, dst)
}

func buildOpusPacketFromFramesInto(scratch *packetScratch, tocBase byte, frames [][]byte, selfDelimited bool, dst []byte) (int, error) {
	count := len(frames)
	if count < 1 || count > 48 {
		return 0, ErrInvalidPacket
	}

	var lengths []int
	if scratch != nil {
		lengths = scratch.lengthSlots(count)
	} else {
		lengths = make([]int, count)
	}
	totalFrameBytes := 0
	for i := range count {
		lengths[i] = len(frames[i])
		totalFrameBytes += lengths[i]
	}

	sdBytes := 0
	if selfDelimited {
		sdBytes = frameLengthBytes(lengths[count-1])
	}

	offset := 0
	switch count {
	case 1:
		need := 1 + sdBytes + lengths[0]
		if len(dst) < need {
			return 0, ErrPacketTooShort
		}
		dst[offset] = tocBase
		offset++
		if selfDelimited {
			offset += writeFrameLength(dst[offset:], lengths[0])
		}
		copy(dst[offset:], frames[0])
		offset += lengths[0]
		return offset, nil

	case 2:
		if lengths[0] == lengths[1] {
			need := 1 + sdBytes + lengths[0] + lengths[1]
			if len(dst) < need {
				return 0, ErrPacketTooShort
			}
			dst[offset] = tocBase | 0x01
			offset++
			if selfDelimited {
				offset += writeFrameLength(dst[offset:], lengths[1])
			}
			copy(dst[offset:], frames[0])
			offset += lengths[0]
			copy(dst[offset:], frames[1])
			offset += lengths[1]
			return offset, nil
		}

		need := 1 + frameLengthBytes(lengths[0]) + sdBytes + lengths[0] + lengths[1]
		if len(dst) < need {
			return 0, ErrPacketTooShort
		}
		dst[offset] = tocBase | 0x02
		offset++
		offset += writeFrameLength(dst[offset:], lengths[0])
		if selfDelimited {
			offset += writeFrameLength(dst[offset:], lengths[1])
		}
		copy(dst[offset:], frames[0])
		offset += lengths[0]
		copy(dst[offset:], frames[1])
		offset += lengths[1]
		return offset, nil
	}

	vbr := false
	for i := 1; i < count; i++ {
		if lengths[i] != lengths[0] {
			vbr = true
			break
		}
	}

	need := 2 + sdBytes + totalFrameBytes
	if vbr {
		for i := 0; i < count-1; i++ {
			need += frameLengthBytes(lengths[i])
		}
	}
	if len(dst) < need {
		return 0, ErrPacketTooShort
	}

	dst[offset] = tocBase | 0x03
	offset++
	if vbr {
		dst[offset] = byte(count) | 0x80
		offset++
		for i := 0; i < count-1; i++ {
			offset += writeFrameLength(dst[offset:], lengths[i])
		}
	} else {
		dst[offset] = byte(count)
		offset++
	}

	if selfDelimited {
		offset += writeFrameLength(dst[offset:], lengths[count-1])
	}

	for i := range count {
		copy(dst[offset:], frames[i])
		offset += lengths[i]
	}

	return offset, nil
}

func buildOpusPacketFromFramesAndPadding(tocBase byte, frames [][]byte, padding []byte, selfDelimited bool, dst []byte) (int, error) {
	return buildOpusPacketFromFramesAndPaddingInto(nil, tocBase, frames, padding, selfDelimited, dst)
}

func buildOpusPacketFromFramesAndPaddingInto(scratch *packetScratch, tocBase byte, frames [][]byte, padding []byte, selfDelimited bool, dst []byte) (int, error) {
	if len(padding) == 0 {
		return buildOpusPacketFromFramesInto(scratch, tocBase, frames, selfDelimited, dst)
	}

	count := len(frames)
	if count < 1 || count > 48 {
		return 0, ErrInvalidPacket
	}

	var lengths []int
	if scratch != nil {
		lengths = scratch.lengthSlots(count)
	} else {
		lengths = make([]int, count)
	}
	totalFrameBytes := 0
	vbr := false
	for i := range count {
		lengths[i] = len(frames[i])
		totalFrameBytes += lengths[i]
		if i > 0 && lengths[i] != lengths[0] {
			vbr = true
		}
	}

	sdBytes := 0
	if selfDelimited {
		sdBytes = frameLengthBytes(lengths[count-1])
	}

	lengthBytes := 0
	if vbr {
		for i := 0; i < count-1; i++ {
			lengthBytes += frameLengthBytes(lengths[i])
		}
	}

	padBytes := paddingLengthBytes(len(padding))
	need := 2 + padBytes + lengthBytes + sdBytes + totalFrameBytes + len(padding)
	if len(dst) < need {
		return 0, ErrPacketTooShort
	}

	offset := 0
	dst[offset] = tocBase | 0x03
	offset++
	countByte := byte(count) | 0x40
	if vbr {
		countByte |= 0x80
	}
	dst[offset] = countByte
	offset++
	offset += writePaddingLength(dst[offset:], len(padding))

	if vbr {
		for i := 0; i < count-1; i++ {
			offset += writeFrameLength(dst[offset:], lengths[i])
		}
	}
	if selfDelimited {
		offset += writeFrameLength(dst[offset:], lengths[count-1])
	}

	for i := range count {
		copy(dst[offset:], frames[i])
		offset += lengths[i]
	}
	copy(dst[offset:], padding)
	offset += len(padding)

	return offset, nil
}

func parsePacketExtensionList(padding []byte, nbFrames int) ([]packetExtensionData, error) {
	if len(padding) == 0 || nbFrames <= 0 {
		return nil, nil
	}

	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, padding, nbFrames)
	var extensions []packetExtensionData
	for {
		var ext packetExtensionData
		ok, err := iter.next(&ext)
		if err != nil {
			return nil, err
		}
		if !ok {
			return extensions, nil
		}
		extensions = append(extensions, ext)
	}
}

func writePacketExtensionPayload(dst []byte, pos int, ext packetExtensionData, last bool) (int, error) {
	if ext.ID < 3 || ext.ID > 127 {
		return 0, ErrInvalidPacket
	}

	if ext.ID < 32 {
		if len(ext.Data) > 1 {
			return 0, ErrInvalidPacket
		}
		if len(dst)-pos < len(ext.Data) {
			return 0, ErrPacketTooShort
		}
		if len(ext.Data) > 0 {
			dst[pos] = ext.Data[0]
			pos++
		}
		return pos, nil
	}

	lengthBytes := 1 + len(ext.Data)/255
	if last {
		lengthBytes = 0
	}
	if len(dst)-pos < lengthBytes+len(ext.Data) {
		return 0, ErrPacketTooShort
	}

	if !last {
		for j := 0; j < len(ext.Data)/255; j++ {
			dst[pos] = 255
			pos++
		}
		dst[pos] = byte(len(ext.Data) % 255)
		pos++
	}
	copy(dst[pos:], ext.Data)
	pos += len(ext.Data)
	return pos, nil
}

func writePacketExtension(dst []byte, pos int, ext packetExtensionData, last bool) (int, error) {
	if ext.ID < 3 || ext.ID > 127 {
		return 0, ErrInvalidPacket
	}
	if len(dst)-pos < 1 {
		return 0, ErrPacketTooShort
	}

	lFlag := 0
	if ext.ID < 32 {
		lFlag = len(ext.Data)
		if lFlag < 0 || lFlag > 1 {
			return 0, ErrInvalidPacket
		}
	} else if !last {
		lFlag = 1
	}

	dst[pos] = byte((ext.ID << 1) | lFlag)
	pos++
	return writePacketExtensionPayload(dst, pos, ext, last)
}

func generatePacketExtensions(dst []byte, length int, extensions []packetExtensionData, nbFrames int, pad bool) (int, error) {
	if nbFrames < 0 || nbFrames > maxPacketExtensionFrames || length < 0 {
		return 0, ErrInvalidPacket
	}
	if dst != nil && len(dst) < length {
		return 0, ErrPacketTooShort
	}

	frameMinIdx := make([]int, nbFrames)
	frameMaxIdx := make([]int, nbFrames)
	frameRepeatIdx := make([]int, nbFrames)
	for f := range nbFrames {
		frameMinIdx[f] = len(extensions)
	}

	for i, ext := range extensions {
		if ext.Frame < 0 || ext.Frame >= nbFrames || ext.ID < 3 || ext.ID > 127 {
			return 0, ErrInvalidPacket
		}
		if i < frameMinIdx[ext.Frame] {
			frameMinIdx[ext.Frame] = i
		}
		if i+1 > frameMaxIdx[ext.Frame] {
			frameMaxIdx[ext.Frame] = i + 1
		}
	}
	copy(frameRepeatIdx, frameMinIdx)

	pos := 0
	written := 0
	currFrame := 0
	for f := range nbFrames {
		lastLongIdx := -1
		repeatCount := 0

		if f+1 < nbFrames {
			for i := frameMinIdx[f]; i < frameMaxIdx[f]; i++ {
				if extensions[i].Frame != f {
					continue
				}

				g := f + 1
				for ; g < nbFrames; g++ {
					if frameRepeatIdx[g] >= frameMaxIdx[g] {
						break
					}
					repeatExt := extensions[frameRepeatIdx[g]]
					if repeatExt.ID != extensions[i].ID {
						break
					}
					if repeatExt.ID < 32 && len(repeatExt.Data) != len(extensions[i].Data) {
						break
					}
				}
				if g < nbFrames {
					break
				}

				if extensions[i].ID >= 32 {
					lastLongIdx = frameRepeatIdx[nbFrames-1]
				}
				for g = f + 1; g < nbFrames; g++ {
					j := frameRepeatIdx[g] + 1
					for ; j < frameMaxIdx[g] && extensions[j].Frame != g; j++ {
					}
					frameRepeatIdx[g] = j
				}
				repeatCount++
				frameRepeatIdx[f] = i
			}
		}

		for i := frameMinIdx[f]; i < frameMaxIdx[f]; i++ {
			if extensions[i].Frame != f {
				continue
			}

			if f != currFrame {
				diff := f - currFrame
				if diff <= 0 {
					return 0, ErrInvalidPacket
				}
				if length-pos < 2 {
					return 0, ErrPacketTooShort
				}
				if dst != nil {
					if diff == 1 {
						dst[pos] = 0x02
					} else {
						dst[pos] = 0x03
						dst[pos+1] = byte(diff)
					}
				}
				if diff == 1 {
					pos++
				} else {
					pos += 2
				}
				currFrame = f
			}

			last := written == len(extensions)-1
			if dst != nil {
				var err error
				pos, err = writePacketExtension(dst, pos, extensions[i], last)
				if err != nil {
					return 0, err
				}
			} else {
				size := 1
				if extensions[i].ID < 32 {
					if len(extensions[i].Data) > 1 {
						return 0, ErrInvalidPacket
					}
					size += len(extensions[i].Data)
				} else {
					if !last {
						size += 1 + len(extensions[i].Data)/255
					}
					size += len(extensions[i].Data)
				}
				if length-pos < size {
					return 0, ErrPacketTooShort
				}
				pos += size
			}
			written++

			if repeatCount > 0 && frameRepeatIdx[f] == i {
				nbRepeated := repeatCount * (nbFrames - (f + 1))
				last := written+nbRepeated == len(extensions) || (lastLongIdx < 0 && i+1 >= frameMaxIdx[f])
				if length-pos < 1 {
					return 0, ErrPacketTooShort
				}
				if dst != nil {
					if last {
						dst[pos] = 0x04
					} else {
						dst[pos] = 0x05
					}
				}
				pos++

				for g := f + 1; g < nbFrames; g++ {
					j := frameMinIdx[g]
					for ; j < frameRepeatIdx[g]; j++ {
						if extensions[j].Frame != g {
							continue
						}
						if dst != nil {
							var err error
							pos, err = writePacketExtensionPayload(dst, pos, extensions[j], last && j == lastLongIdx)
							if err != nil {
								return 0, err
							}
						} else {
							size := len(extensions[j].Data)
							if extensions[j].ID < 32 {
								if size > 1 {
									return 0, ErrInvalidPacket
								}
							} else if !last || j != lastLongIdx {
								size += 1 + len(extensions[j].Data)/255
							}
							if length-pos < size {
								return 0, ErrPacketTooShort
							}
							pos += size
						}
						written++
					}
					frameMinIdx[g] = j
				}
				if last {
					currFrame++
				}
			}
		}
	}

	if written != len(extensions) {
		return 0, ErrInvalidPacket
	}

	if pad && pos < length {
		padding := length - pos
		if dst != nil {
			copy(dst[padding:], dst[:pos])
			for i := range padding {
				dst[i] = 0x01
			}
		}
		pos += padding
	}

	return pos, nil
}

func buildOpusPacketFromFramesAndExtensions(tocBase byte, frames [][]byte, extensions []packetExtensionData, selfDelimited bool, dst []byte) (int, error) {
	if len(extensions) == 0 {
		return buildOpusPacketFromFrames(tocBase, frames, selfDelimited, dst)
	}

	extLen, err := generatePacketExtensions(nil, len(dst), extensions, len(frames), false)
	if err != nil {
		return 0, err
	}
	padding := make([]byte, extLen)
	if _, err := generatePacketExtensions(padding, extLen, extensions, len(frames), false); err != nil {
		return 0, err
	}
	return buildOpusPacketFromFramesAndPadding(tocBase, frames, padding, selfDelimited, dst)
}

func makeSelfDelimitedPacket(packet []byte) ([]byte, error) {
	// Self-delimiting framing adds at most 2 bytes.
	dst := make([]byte, len(packet)+2)
	n, err := makeSelfDelimitedPacketInto(nil, dst, packet)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

// makeSelfDelimitedPacketInto reframes a standard packet to self-delimited form
// into dst (which must hold at least len(packet)+2 bytes) and returns the number
// of bytes written. scratch may be nil. Ordinary (non-extension) padding is
// dropped, matching makeSelfDelimitedPacket; only opaque packet extensions are
// re-emitted. The common path (no padding, or padding carrying no extensions)
// is allocation-free.
func makeSelfDelimitedPacketInto(scratch *packetScratch, dst, packet []byte) (int, error) {
	parsed, err := parseOpusPacketInto(scratch, packet, false)
	if err != nil {
		return 0, err
	}
	if len(parsed.padding) == 0 {
		return buildOpusPacketFromFramesInto(scratch, parsed.tocBase, parsed.frames, true, dst)
	}
	extensions, err := parsePacketExtensionList(parsed.padding, parsed.paddingFrameCount)
	if err != nil {
		return 0, err
	}
	if len(extensions) == 0 {
		// Ordinary padding carries no extensions; drop it entirely.
		return buildOpusPacketFromFramesInto(scratch, parsed.tocBase, parsed.frames, true, dst)
	}
	return buildOpusPacketFromFramesAndExtensions(parsed.tocBase, parsed.frames, extensions, true, dst)
}

func decodeSelfDelimitedPacket(data []byte) ([]byte, int, error) {
	parsed, err := parseOpusPacket(data, true)
	if err != nil {
		return nil, 0, err
	}

	dst := make([]byte, parsed.consumed)
	n, err := buildOpusPacketFromFramesAndPadding(parsed.tocBase, parsed.frames, parsed.padding, false, dst)
	if err != nil {
		return nil, 0, err
	}
	return dst[:n], parsed.consumed, nil
}

// decodeSelfDelimitedPacketInto reframes the leading self-delimited packet of
// data into standard form written to dst (which must hold at least the consumed
// byte count). It returns the bytes written, the bytes consumed from data, and
// any error. scratch may be nil for the legacy allocating parser behavior.
func decodeSelfDelimitedPacketInto(scratch *packetScratch, dst, data []byte) (written, consumed int, err error) {
	parsed, perr := parseOpusPacketInto(scratch, data, true)
	if perr != nil {
		return 0, 0, perr
	}
	n, berr := buildOpusPacketFromFramesAndPaddingInto(scratch, parsed.tocBase, parsed.frames, parsed.padding, false, dst)
	if berr != nil {
		return 0, 0, berr
	}
	return n, parsed.consumed, nil
}

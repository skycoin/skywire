package gopus

const (
	maxRepacketizerFrames      = 48
	maxRepacketizerDuration48k = 5760 // 120ms at 48kHz
)

// Repacketizer copies frames from Opus packets and assembles packets from
// contiguous frame ranges. Added packets must have the same TOC configuration
// and channel flag (bits 7 through 2); their frame-count codes may differ. The
// accumulator accepts at most 48 frames and 120 ms of audio. Repacketizer is not
// safe for concurrent use.
type Repacketizer struct {
	toc       byte
	frameSize int
	frames    [][]byte
	paddings  [][]byte
	padFrames []int
}

// NewRepacketizer creates an empty repacketizer.
func NewRepacketizer() *Repacketizer {
	r := &Repacketizer{
		frames:    make([][]byte, 0, maxRepacketizerFrames),
		paddings:  make([][]byte, 0, maxRepacketizerFrames),
		padFrames: make([]int, 0, maxRepacketizerFrames),
	}
	r.Reset()
	return r
}

// Reset discards all accumulated frames so the repacketizer can be reused.
func (r *Repacketizer) Reset() {
	r.toc = 0
	r.frameSize = 0
	clear(r.frames)
	clear(r.paddings)
	r.frames = r.frames[:0]
	r.paddings = r.paddings[:0]
	r.padFrames = r.padFrames[:0]
}

// NumFrames returns the number of accumulated audio frames, not the number of
// packets passed to Cat.
func (r *Repacketizer) NumFrames() int {
	return len(r.frames)
}

// Cat parses packet and copies its encoded frame data into the accumulator.
// The caller may reuse packet after Cat returns. It returns an error for an
// invalid packet, incompatible TOC configuration or channel flag, or an
// accumulator that would exceed its frame-count or duration limits.
func (r *Repacketizer) Cat(packet []byte) error {
	if len(packet) < 1 {
		return ErrInvalidPacket
	}

	var frameSizes [maxRepacketizerFrames]int
	info, err := parsePacketInto(packet, &frameSizes)
	if err != nil {
		return err
	}
	if info.Padding > len(packet) {
		return ErrInvalidPacket
	}
	if info.FrameCount == 0 {
		return ErrInvalidPacket
	}
	paddingBytes := packet[len(packet)-info.Padding:]
	frameBytes := 0
	for _, frameSize := range info.FrameSizes {
		frameBytes += frameSize
	}
	frameOffset := len(packet) - info.Padding - frameBytes

	if len(r.frames) == 0 {
		r.toc = packet[0]
		r.frameSize = info.TOC.FrameSize
	} else if (r.toc & 0xFC) != (packet[0] & 0xFC) {
		return ErrInvalidPacket
	}

	totalFrames := len(r.frames) + info.FrameCount
	if totalFrames > maxRepacketizerFrames {
		return ErrInvalidPacket
	}
	if totalFrames*r.frameSize > maxRepacketizerDuration48k {
		return ErrInvalidPacket
	}

	paddingFrameCount := 0
	if info.Padding > 0 {
		paddingFrameCount = info.FrameCount
	}
	for i, frameSize := range info.FrameSizes {
		frame := packet[frameOffset : frameOffset+frameSize]
		owned := make([]byte, len(frame))
		copy(owned, frame)
		r.frames = append(r.frames, owned)
		if i == 0 && len(paddingBytes) > 0 {
			ownedPadding := make([]byte, len(paddingBytes))
			copy(ownedPadding, paddingBytes)
			r.paddings = append(r.paddings, ownedPadding)
			r.padFrames = append(r.padFrames, paddingFrameCount)
		} else {
			r.paddings = append(r.paddings, nil)
			r.padFrames = append(r.padFrames, 0)
		}
		frameOffset += frameSize
	}

	return nil
}

// OutRange assembles the accumulated frame range [begin, end) into one Opus
// packet. end is exclusive. data must have enough length for the output; the
// returned count is the number of bytes written. It returns ErrInvalidArgument
// for an empty or out-of-range range, ErrInternalError for malformed packet
// extensions, and ErrBufferTooSmall when data is short.
func (r *Repacketizer) OutRange(begin, end int, data []byte) (int, error) {
	if begin < 0 || begin >= end || end > len(r.frames) {
		return 0, ErrInvalidArgument
	}
	extensions, err := r.collectExtensions(begin, end)
	if err != nil {
		return 0, ErrInternalError
	}
	return buildRepacketizedPacketWithOptions(r.toc&0xFC, r.frames[begin:end], data, 0, false, extensions)
}

// Out assembles all accumulated frames into one Opus packet. data must have
// enough length for the output packet; the returned count is the number of bytes
// written.
func (r *Repacketizer) Out(data []byte) (int, error) {
	return r.OutRange(0, len(r.frames), data)
}

// PacketPad pads a packet in place to exactly newLen bytes.
// length is the current packet length in bytes. data must contain length bytes
// and have capacity for newLen bytes. If len(data) is shorter, reslice the
// caller's slice to newLen after success. It returns ErrInternalError when
// packet extension data cannot be collected.
func PacketPad(data []byte, length, newLen int) error {
	if length < 1 || length > len(data) || newLen < length {
		return ErrInvalidArgument
	}
	if newLen == length {
		return nil
	}
	if newLen > cap(data) {
		return ErrBufferTooSmall
	}
	data = data[:newLen]

	var frameSizes [maxRepacketizerFrames]int
	var frames [maxRepacketizerFrames][]byte
	_, frameCount, padding, err := parsePacketFramesInto(data[:length], &frameSizes, &frames)
	if err != nil {
		return err
	}

	var paddingBytes []byte
	if padding > 0 {
		paddingBytes = data[length-padding : length]
	}
	extensionCount, err := countPacketExtensions(paddingBytes, frameCount)
	if err != nil {
		return ErrInternalError
	}
	if extensionCount == 0 {
		return packetPadWithoutExtensions(data, length, newLen, data[0]&0xFC, padding, frameSizes[:frameCount], frames[:frameCount])
	}

	// Extension-bearing packets use a snapshot because the rebuilt header can
	// overlap the packet's original frame payloads.
	src := make([]byte, length)
	copy(src, data[:length])
	frameBytes := 0
	for _, size := range frameSizes[:frameCount] {
		frameBytes += size
	}
	srcOffset := length - padding - frameBytes
	framesWithExtensions := frames[:frameCount]
	for i, size := range frameSizes[:frameCount] {
		framesWithExtensions[i] = src[srcOffset : srcOffset+size]
		srcOffset += size
	}
	if padding > 0 {
		paddingBytes = src[length-padding:]
	}

	extensions := make([]packetExtensionData, extensionCount)
	extensionCount, err = parsePacketExtensions(paddingBytes, frameCount, extensions)
	if err != nil {
		return ErrInternalError
	}
	extensions = extensions[:extensionCount]

	_, err = buildRepacketizedPacketWithOptions(src[0]&0xFC, framesWithExtensions, data, newLen, true, extensions)
	return err
}

// packetPadWithoutExtensions relocates the contiguous frame payload with Go's
// overlap-safe copy before rebuilding the code 3 framing in the same buffer.
func packetPadWithoutExtensions(data []byte, packetLen, newLen int, tocBase byte, padding int, frameSizes []int, frames [][]byte) error {
	frameBytes := 0
	vbr := false
	for i, size := range frameSizes {
		frameBytes += size
		if i > 0 && size != frameSizes[0] {
			vbr = true
		}
	}

	lengthBytes := 0
	if vbr {
		for _, size := range frameSizes[:len(frameSizes)-1] {
			lengthBytes += frameLengthBytes(size)
		}
	}
	baseLen := 2 + lengthBytes + frameBytes
	if newLen < baseLen {
		return ErrBufferTooSmall
	}
	paddingAmount := newLen - baseLen
	payloadStart := 2 + lengthBytes + paddingLengthBytes(paddingAmount)
	sourceStart := packetLen - padding - frameBytes
	copy(data[payloadStart:payloadStart+frameBytes], data[sourceStart:sourceStart+frameBytes])

	offset := payloadStart
	for i, size := range frameSizes {
		frames[i] = data[offset : offset+size]
		offset += size
	}
	_, err := buildRepacketizedPacketWithOptions(tocBase, frames, data, newLen, true, nil)
	return err
}

// PacketUnpad removes packet padding in place and returns the new packet length
// in bytes. length is the current packet length; data must contain that many
// bytes. Use data[:n] with the returned length n to access the unpadded packet.
func PacketUnpad(data []byte, length int) (int, error) {
	if length < 1 || length > len(data) {
		return 0, ErrInvalidArgument
	}

	var frameSizes [maxRepacketizerFrames]int
	var frames [maxRepacketizerFrames][]byte
	_, frameCount, _, err := parsePacketFramesInto(data[:length], &frameSizes, &frames)
	if err != nil {
		return 0, err
	}

	return buildRepacketizedPacket(data[0]&0xFC, frames[:frameCount], data[:length])
}

func parseSelfDelimitedPacket(data []byte) (tocBase byte, frames [][]byte, consumed int, err error) {
	tocBase, frames, _, _, consumed, err = parseSelfDelimitedPacketAndPadding(data)
	return tocBase, frames, consumed, err
}

func parseSelfDelimitedPacketAndPadding(data []byte) (tocBase byte, frames [][]byte, padding []byte, paddingFrameCount int, consumed int, err error) {
	if len(data) < 1 {
		return 0, nil, nil, 0, 0, ErrPacketTooShort
	}

	toc := data[0]
	code := toc & 0x03
	offset := 1
	paddingLen := 0
	frameCount := 1
	frameSizes := make([]int, 0, 2)

	switch code {
	case 0:
		length, bytesRead, err := parseFrameLength(data, offset)
		if err != nil {
			return 0, nil, nil, 0, 0, ErrInvalidPacket
		}
		offset += bytesRead
		frameSizes = append(frameSizes, length)

	case 1:
		length, bytesRead, err := parseFrameLength(data, offset)
		if err != nil {
			return 0, nil, nil, 0, 0, ErrInvalidPacket
		}
		offset += bytesRead
		frameCount = 2
		frameSizes = append(frameSizes, length, length)

	case 2:
		length0, bytesRead, err := parseFrameLength(data, offset)
		if err != nil {
			return 0, nil, nil, 0, 0, ErrInvalidPacket
		}
		offset += bytesRead

		length1, bytesRead, err := parseFrameLength(data, offset)
		if err != nil {
			return 0, nil, nil, 0, 0, ErrInvalidPacket
		}
		offset += bytesRead

		frameCount = 2
		frameSizes = append(frameSizes, length0, length1)

	case 3:
		if offset >= len(data) {
			return 0, nil, nil, 0, 0, ErrInvalidPacket
		}
		frameCountByte := data[offset]
		offset++

		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		frameCount = int(frameCountByte & 0x3F)
		if frameCount == 0 || frameCount > maxRepacketizerFrames {
			return 0, nil, nil, 0, 0, ErrInvalidPacket
		}
		if ParseTOC(toc).FrameSize*frameCount > maxRepacketizerDuration48k {
			return 0, nil, nil, 0, 0, ErrInvalidPacket
		}

		frameSizes = make([]int, frameCount)
		if hasPadding {
			for {
				if offset >= len(data) {
					return 0, nil, nil, 0, 0, ErrInvalidPacket
				}
				padByte := int(data[offset])
				offset++
				if padByte == 255 {
					paddingLen += 254
				} else {
					paddingLen += padByte
					break
				}
			}
		}

		if vbr {
			for i := 0; i < frameCount-1; i++ {
				length, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return 0, nil, nil, 0, 0, ErrInvalidPacket
				}
				offset += bytesRead
				frameSizes[i] = length
			}
		}

		lastSize, bytesRead, err := parseFrameLength(data, offset)
		if err != nil {
			return 0, nil, nil, 0, 0, ErrInvalidPacket
		}
		offset += bytesRead

		if vbr {
			frameSizes[frameCount-1] = lastSize
		} else {
			for i := 0; i < frameCount; i++ {
				frameSizes[i] = lastSize
			}
		}

	default:
		return 0, nil, nil, 0, 0, ErrInvalidPacket
	}

	totalFrameBytes := 0
	for _, size := range frameSizes {
		if size < 0 {
			return 0, nil, nil, 0, 0, ErrInvalidPacket
		}
		totalFrameBytes += size
	}

	consumed = offset + totalFrameBytes + paddingLen
	if consumed > len(data) {
		return 0, nil, nil, 0, 0, ErrInvalidPacket
	}

	frames = make([][]byte, frameCount)
	frameOffset := offset
	paddingStart := offset + totalFrameBytes
	for i := 0; i < frameCount; i++ {
		next := frameOffset + frameSizes[i]
		if next > offset+totalFrameBytes {
			return 0, nil, nil, 0, 0, ErrInvalidPacket
		}
		frames[i] = data[frameOffset:next]
		frameOffset = next
	}

	if paddingLen > 0 {
		padding = data[paddingStart:consumed]
	}
	return toc & 0xFC, frames, padding, frameCount, consumed, nil
}

func buildSelfDelimitedPacketFromFramesAndOptions(tocBase byte, frames [][]byte, data []byte, targetLen int, withPadding bool, extensions []packetExtensionData) (int, error) {
	return buildPacketWithOptions(tocBase, frames, data, targetLen, withPadding, extensions, true)
}

func buildPacketWithOptions(tocBase byte, frames [][]byte, data []byte, targetLen int, withPadding bool, extensions []packetExtensionData, selfDelimited bool) (int, error) {
	count := len(frames)
	if count < 1 || count > maxRepacketizerFrames {
		return 0, ErrInvalidArgument
	}
	if len(extensions) == 0 && !withPadding {
		switch count {
		case 1:
			need := 1 + len(frames[0])
			if selfDelimited {
				need += frameLengthBytes(len(frames[0]))
			}
			if len(data) < need {
				return 0, ErrBufferTooSmall
			}
			data[0] = tocBase
			offset := 1
			if selfDelimited {
				offset += encodeFrameLength(data[offset:], len(frames[0]))
			}
			copy(data[offset:], frames[0])
			return need, nil
		case 2:
			if len(frames[0]) == len(frames[1]) {
				need := 1 + len(frames[0]) + len(frames[1])
				if selfDelimited {
					need += frameLengthBytes(len(frames[1]))
				}
				if len(data) < need {
					return 0, ErrBufferTooSmall
				}
				data[0] = tocBase | 0x01
				offset := 1
				if selfDelimited {
					offset += encodeFrameLength(data[offset:], len(frames[1]))
				}
				copy(data[offset:], frames[0])
				offset += len(frames[0])
				copy(data[offset:], frames[1])
				return need, nil
			}

			need := 1 + frameLengthBytes(len(frames[0])) + len(frames[0]) + len(frames[1])
			if selfDelimited {
				need += frameLengthBytes(len(frames[1]))
			}
			if len(data) < need {
				return 0, ErrBufferTooSmall
			}
			data[0] = tocBase | 0x02
			offset := 1
			offset += encodeFrameLength(data[offset:], len(frames[0]))
			if selfDelimited {
				offset += encodeFrameLength(data[offset:], len(frames[1]))
			}
			copy(data[offset:], frames[0])
			offset += len(frames[0])
			copy(data[offset:], frames[1])
			return need, nil
		}
	}
	return buildCode3Packet(tocBase, frames, data, targetLen, withPadding, extensions, selfDelimited)
}

func parsePacketFrames(data []byte) (PacketInfo, [][]byte, error) {
	info, err := ParsePacket(data)
	if err != nil {
		return PacketInfo{}, nil, err
	}

	frames := make([][]byte, info.FrameCount)
	if err := fillPacketFrameSlices(data, info, frames); err != nil {
		return PacketInfo{}, nil, err
	}
	return info, frames, nil
}

func parsePacketFramesInto(data []byte, frameSizes *[maxRepacketizerFrames]int, frames *[maxRepacketizerFrames][]byte) (toc TOC, frameCount, padding int, err error) {
	info, err := parsePacketInto(data, frameSizes)
	if err != nil {
		return TOC{}, 0, 0, err
	}
	toc, frameCount, padding = info.TOC, info.FrameCount, info.Padding
	if err := fillPacketFrameSlices(data, info, frames[:frameCount]); err != nil {
		return TOC{}, 0, 0, err
	}
	return toc, frameCount, padding, nil
}

func fillPacketFrameSlices(data []byte, info PacketInfo, frames [][]byte) error {
	switch info.TOC.FrameCode {
	case 0:
		if len(data) < 1+info.FrameSizes[0] {
			return ErrInvalidPacket
		}
		frames[0] = data[1 : 1+info.FrameSizes[0]]
	case 1:
		offset := 1
		for i := 0; i < info.FrameCount; i++ {
			frameLen := info.FrameSizes[i]
			if offset+frameLen > len(data) {
				return ErrInvalidPacket
			}
			frames[i] = data[offset : offset+frameLen]
			offset += frameLen
		}
	case 2:
		frame1Len, bytesRead, err := parseFrameLength(data, 1)
		if err != nil {
			return err
		}
		headerLen := 1 + bytesRead
		if frame1Len != info.FrameSizes[0] {
			return ErrInvalidPacket
		}
		if headerLen+info.FrameSizes[0]+info.FrameSizes[1] > len(data) {
			return ErrInvalidPacket
		}
		frames[0] = data[headerLen : headerLen+info.FrameSizes[0]]
		frames[1] = data[headerLen+info.FrameSizes[0] : headerLen+info.FrameSizes[0]+info.FrameSizes[1]]
	case 3:
		if len(data) < 2 {
			return ErrPacketTooShort
		}
		frameCountByte := data[1]
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0

		offset := 2
		padding := 0
		if hasPadding {
			for {
				if offset >= len(data) {
					return ErrPacketTooShort
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

		if vbr {
			for i := 0; i < info.FrameCount-1; i++ {
				_, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return err
				}
				offset += bytesRead
			}
		}

		frameOffset := offset
		frameDataEnd := len(data) - padding
		for i := 0; i < info.FrameCount; i++ {
			frameLen := info.FrameSizes[i]
			if frameLen < 0 || frameOffset+frameLen > frameDataEnd {
				return ErrInvalidPacket
			}
			frames[i] = data[frameOffset : frameOffset+frameLen]
			frameOffset += frameLen
		}
	default:
		return ErrInvalidPacket
	}

	return nil
}

func parsePacketFramesAndPadding(data []byte) (PacketInfo, [][]byte, []byte, int, error) {
	info, frames, err := parsePacketFrames(data)
	if err != nil {
		return PacketInfo{}, nil, nil, 0, err
	}
	if info.Padding == 0 {
		return info, frames, nil, 0, nil
	}
	if info.Padding > len(data) {
		return PacketInfo{}, nil, nil, 0, ErrInvalidPacket
	}
	return info, frames, data[len(data)-info.Padding:], info.FrameCount, nil
}

func buildRepacketizedPacket(tocBase byte, frames [][]byte, data []byte) (int, error) {
	return buildRepacketizedPacketWithOptions(tocBase, frames, data, 0, false, nil)
}

func buildRepacketizedPacketWithOptions(tocBase byte, frames [][]byte, data []byte, targetLen int, withPadding bool, extensions []packetExtensionData) (int, error) {
	return buildPacketWithOptions(tocBase, frames, data, targetLen, withPadding, extensions, false)
}

func buildCode3Packet(tocBase byte, frames [][]byte, data []byte, targetLen int, withPadding bool, extensions []packetExtensionData, selfDelimited bool) (int, error) {
	count := len(frames)
	if count < 1 || count > maxRepacketizerFrames {
		return 0, ErrInvalidArgument
	}

	vbr := false
	for i := 1; i < count; i++ {
		if len(frames[i]) != len(frames[0]) {
			vbr = true
			break
		}
	}

	lengthBytes := 0
	if vbr {
		for i := 0; i < count-1; i++ {
			lengthBytes += frameLengthBytes(len(frames[i]))
		}
	}

	frameBytes := 0
	for _, frame := range frames {
		frameBytes += len(frame)
	}

	baseLen := 2 + lengthBytes + frameBytes
	if selfDelimited {
		baseLen += frameLengthBytes(len(frames[count-1]))
	}
	need := baseLen
	paddingAmount := 0
	extLen := 0
	extBegin := 0
	onesBegin := 0
	onesEnd := 0
	maxLen := len(data)
	if withPadding {
		if targetLen < baseLen {
			return 0, ErrBufferTooSmall
		}
		maxLen = targetLen
	} else if len(data) < baseLen {
		return 0, ErrBufferTooSmall
	}

	if len(extensions) > 0 {
		var err error
		extLen, err = generatePacketExtensions(nil, maxLen-baseLen, extensions, count, false)
		if err != nil {
			return 0, err
		}
		if !withPadding {
			paddingAmount = extLen
			if extLen > 0 {
				paddingAmount += (extLen + 253) / 254
			} else {
				paddingAmount++
			}
		}
	}

	if withPadding {
		paddingAmount = targetLen - baseLen
	}
	if paddingAmount != 0 {
		padFieldBytes := paddingLengthBytes(paddingAmount)
		if baseLen+extLen+padFieldBytes > maxLen {
			return 0, ErrBufferTooSmall
		}
		need = baseLen + paddingAmount
		extBegin = baseLen + paddingAmount - extLen
		onesBegin = baseLen + padFieldBytes
		onesEnd = baseLen + paddingAmount - extLen
	}
	if len(data) < need {
		return 0, ErrBufferTooSmall
	}

	offset := 0
	data[offset] = tocBase | 0x03
	offset++

	countByte := byte(count & 0x3F)
	if vbr {
		countByte |= 0x80
	}
	if paddingAmount != 0 {
		countByte |= 0x40
	}
	data[offset] = countByte
	offset++

	if paddingAmount != 0 {
		offset += writePaddingLength(data[offset:], paddingAmount)
	}

	if vbr {
		for i := 0; i < count-1; i++ {
			offset += encodeFrameLength(data[offset:], len(frames[i]))
		}
	}
	if selfDelimited {
		offset += encodeFrameLength(data[offset:], len(frames[count-1]))
	}

	for _, frame := range frames {
		copy(data[offset:], frame)
		offset += len(frame)
	}

	if extLen > 0 {
		if _, err := generatePacketExtensions(data[extBegin:extBegin+extLen], extLen, extensions, count, false); err != nil {
			return 0, err
		}
	}
	for i := onesBegin; i < onesEnd; i++ {
		data[i] = 0x01
	}
	if withPadding && len(extensions) == 0 {
		for i := offset; i < need; i++ {
			data[i] = 0
		}
	}

	return need, nil
}

func frameLengthBytes(size int) int {
	if size < 252 {
		return 1
	}
	return 2
}

func encodeFrameLength(dst []byte, size int) int {
	if size < 252 {
		dst[0] = byte(size)
		return 1
	}
	first := 252 + (size & 0x03)
	second := (size - first) / 4
	dst[0] = byte(first)
	dst[1] = byte(second)
	return 2
}

func paddingLengthBytes(extra int) int {
	if extra <= 0 {
		return 0
	}
	return (extra-1)/255 + 1
}

func writePaddingLength(dst []byte, extra int) int {
	w := 0
	remaining := extra
	for remaining > 255 {
		dst[w] = 255
		w++
		remaining -= 255
	}
	dst[w] = byte(remaining - 1)
	return w + 1
}

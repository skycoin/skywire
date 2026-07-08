// This file provides packet assembly functions for combining TOC bytes with
// encoded frame data to create complete Opus packets per RFC 6716 Section 3.

package encoder

import (
	"errors"

	"github.com/thesyncim/gopus/types"
)

const qextExtensionID = 124

type packetExtension struct {
	ID    int
	Data  []byte
	Frame int
}

// Packet assembly errors.
var (
	ErrInvalidConfig     = errors.New("encoder: invalid config for mode/bandwidth/frameSize")
	ErrInvalidFrameCount = errors.New("encoder: frame count must be 1-48")
)

// configEntry holds the mode, bandwidth, and frame size for a configuration.
type configEntry struct {
	Mode      types.Mode
	Bandwidth types.Bandwidth
	FrameSize int // In samples at 48kHz
}

// configTable maps configuration indices 0-31 to their properties.
// Based on RFC 6716 Section 3.1 Table.
var configTable = [32]configEntry{
	// SILK-only NB: configs 0-3 (10/20/40/60ms)
	{types.ModeSILK, types.BandwidthNarrowband, 480},  // 0: 10ms
	{types.ModeSILK, types.BandwidthNarrowband, 960},  // 1: 20ms
	{types.ModeSILK, types.BandwidthNarrowband, 1920}, // 2: 40ms
	{types.ModeSILK, types.BandwidthNarrowband, 2880}, // 3: 60ms
	// SILK-only MB: configs 4-7
	{types.ModeSILK, types.BandwidthMediumband, 480},  // 4
	{types.ModeSILK, types.BandwidthMediumband, 960},  // 5
	{types.ModeSILK, types.BandwidthMediumband, 1920}, // 6
	{types.ModeSILK, types.BandwidthMediumband, 2880}, // 7
	// SILK-only WB: configs 8-11
	{types.ModeSILK, types.BandwidthWideband, 480},  // 8
	{types.ModeSILK, types.BandwidthWideband, 960},  // 9
	{types.ModeSILK, types.BandwidthWideband, 1920}, // 10
	{types.ModeSILK, types.BandwidthWideband, 2880}, // 11
	// Hybrid SWB: configs 12-13
	{types.ModeHybrid, types.BandwidthSuperwideband, 480}, // 12: 10ms
	{types.ModeHybrid, types.BandwidthSuperwideband, 960}, // 13: 20ms
	// Hybrid FB: configs 14-15
	{types.ModeHybrid, types.BandwidthFullband, 480}, // 14
	{types.ModeHybrid, types.BandwidthFullband, 960}, // 15
	// CELT NB: configs 16-19 (2.5/5/10/20ms)
	{types.ModeCELT, types.BandwidthNarrowband, 120}, // 16: 2.5ms
	{types.ModeCELT, types.BandwidthNarrowband, 240}, // 17: 5ms
	{types.ModeCELT, types.BandwidthNarrowband, 480}, // 18: 10ms
	{types.ModeCELT, types.BandwidthNarrowband, 960}, // 19: 20ms
	// CELT WB: configs 20-23
	{types.ModeCELT, types.BandwidthWideband, 120}, // 20
	{types.ModeCELT, types.BandwidthWideband, 240}, // 21
	{types.ModeCELT, types.BandwidthWideband, 480}, // 22
	{types.ModeCELT, types.BandwidthWideband, 960}, // 23
	// CELT SWB: configs 24-27
	{types.ModeCELT, types.BandwidthSuperwideband, 120}, // 24
	{types.ModeCELT, types.BandwidthSuperwideband, 240}, // 25
	{types.ModeCELT, types.BandwidthSuperwideband, 480}, // 26
	{types.ModeCELT, types.BandwidthSuperwideband, 960}, // 27
	// CELT FB: configs 28-31
	{types.ModeCELT, types.BandwidthFullband, 120}, // 28
	{types.ModeCELT, types.BandwidthFullband, 240}, // 29
	{types.ModeCELT, types.BandwidthFullband, 480}, // 30
	{types.ModeCELT, types.BandwidthFullband, 960}, // 31
}

// configFromParams returns the config index for given mode, bandwidth, and frame size.
// Returns -1 if the combination is invalid.
func configFromParams(mode types.Mode, bandwidth types.Bandwidth, frameSize int) int {
	for i, entry := range configTable {
		if entry.Mode == mode && entry.Bandwidth == bandwidth && entry.FrameSize == frameSize {
			return i
		}
	}
	return -1
}

// generateTOC creates a TOC byte from encoding parameters.
func generateTOC(config uint8, stereo bool, frameCode uint8) byte {
	toc := (config & 0x1F) << 3
	if stereo {
		toc |= 0x04
	}
	toc |= frameCode & 0x03
	return toc
}

// BuildPacketInto creates a complete Opus packet into the provided buffer.
// Returns the number of bytes written, or error if buffer is too small.
// Uses frame code 0 (single frame).
func BuildPacketInto(dst, frameData []byte, mode types.Mode, bandwidth types.Bandwidth, frameSize int, stereo bool) (int, error) {
	config := configFromParams(mode, bandwidth, frameSize)
	if config < 0 {
		return 0, ErrInvalidConfig
	}

	needed := 1 + len(frameData)
	if len(dst) < needed {
		return 0, ErrInvalidConfig // buffer too small
	}

	toc := generateTOC(uint8(config), stereo, 0)

	// Packet = TOC + frame data
	dst[0] = toc
	copy(dst[1:], frameData)

	return needed, nil
}

func buildPacketWithSingleExtensionInto(dst, frameData []byte, mode types.Mode, bandwidth types.Bandwidth, frameSize int, stereo bool, extID int, extData []byte, targetLen int, withPadding bool) (int, error) {
	return buildPacketWithExtensionsInto(dst, frameData, mode, bandwidth, frameSize, stereo, []packetExtension{{ID: extID, Data: extData}}, targetLen, withPadding)
}

func buildMultiFramePacketWithExtensionsInto(dst []byte, frames [][]byte, mode types.Mode, bandwidth types.Bandwidth, frameSize int, stereo bool, vbr bool, extensions []packetExtension, targetLen int, withPadding bool) (int, error) {
	if len(frames) == 0 || len(frames) > 48 {
		return 0, ErrInvalidFrameCount
	}

	config := configFromParams(mode, bandwidth, frameSize)
	if config < 0 {
		return 0, ErrInvalidConfig
	}
	for _, ext := range extensions {
		if ext.ID < 3 || ext.ID > 127 || ext.Frame < 0 || ext.Frame >= len(frames) {
			return 0, ErrInvalidConfig
		}
	}

	baseLen := 2
	if vbr {
		for i := 0; i < len(frames)-1; i++ {
			baseLen += frameLengthBytes(len(frames[i]))
		}
	}
	for _, frame := range frames {
		baseLen += len(frame)
	}

	need := baseLen
	maxLen := len(dst)
	if withPadding {
		if targetLen < baseLen+1 {
			return 0, ErrInvalidConfig
		}
		maxLen = targetLen
	}

	extLen := 0
	if len(extensions) > 0 {
		var err error
		extLen, err = generatePacketExtensions(nil, maxLen-baseLen, extensions, len(frames), false)
		if err != nil {
			return 0, err
		}
	}
	paddingAmount := 0
	extBegin := 0
	onesBegin := 0
	onesEnd := 0

	if len(extensions) > 0 && !withPadding {
		paddingAmount = extLen + (extLen+253)/254
	}
	if withPadding {
		paddingAmount = targetLen - baseLen
	}
	if paddingAmount != 0 {
		padFieldBytes := paddingLengthBytes(paddingAmount)
		if baseLen+extLen+padFieldBytes > maxLen {
			return 0, ErrInvalidConfig
		}
		need = baseLen + paddingAmount
		extBegin = baseLen + paddingAmount - extLen
		onesBegin = baseLen + padFieldBytes
		onesEnd = baseLen + paddingAmount - extLen
	}
	if len(dst) < need {
		return 0, ErrInvalidConfig
	}

	offset := 0
	dst[offset] = generateTOC(uint8(config), stereo, 3)
	offset++

	countByte := byte(len(frames) & 0x3F)
	if vbr {
		countByte |= 0x80
	}
	if paddingAmount != 0 {
		countByte |= 0x40
	}
	dst[offset] = countByte
	offset++

	if paddingAmount != 0 {
		offset += writePaddingLength(dst[offset:], paddingAmount)
	}
	if vbr {
		for i := 0; i < len(frames)-1; i++ {
			offset += writeFrameLength(dst[offset:], len(frames[i]))
		}
	}
	for _, frame := range frames {
		copy(dst[offset:], frame)
		offset += len(frame)
	}
	if extLen > 0 {
		if _, err := generatePacketExtensions(dst[extBegin:extBegin+extLen], extLen, extensions, len(frames), false); err != nil {
			return 0, err
		}
	}
	for i := onesBegin; i < onesEnd; i++ {
		dst[i] = 0x01
	}

	return need, nil
}

func buildPacketWithExtensionsInto(dst, frameData []byte, mode types.Mode, bandwidth types.Bandwidth, frameSize int, stereo bool, extensions []packetExtension, targetLen int, withPadding bool) (int, error) {
	config := configFromParams(mode, bandwidth, frameSize)
	if config < 0 {
		return 0, ErrInvalidConfig
	}

	for _, ext := range extensions {
		if ext.ID < 3 || ext.ID > 127 {
			return 0, ErrInvalidConfig
		}
	}

	baseLen := 2 + len(frameData)
	need := baseLen
	maxLen := len(dst)
	if withPadding {
		if targetLen < baseLen+1 {
			return 0, ErrInvalidConfig
		}
		maxLen = targetLen
	}

	extLen := 0
	for i, ext := range extensions {
		extLen += packetExtensionLength(ext.ID, ext.Data, i == len(extensions)-1)
	}
	paddingAmount := 0
	extBegin := 0
	onesBegin := 0
	onesEnd := 0

	if extLen > 0 && !withPadding {
		paddingAmount = extLen + (extLen+253)/254
	}
	if withPadding {
		paddingAmount = targetLen - baseLen
	}
	if paddingAmount != 0 {
		padFieldBytes := paddingLengthBytes(paddingAmount)
		if baseLen+extLen+padFieldBytes > maxLen {
			return 0, ErrInvalidConfig
		}
		need = baseLen + paddingAmount
		extBegin = baseLen + paddingAmount - extLen
		onesBegin = baseLen + padFieldBytes
		onesEnd = baseLen + paddingAmount - extLen
	}
	if len(dst) < need {
		return 0, ErrInvalidConfig
	}

	offset := 0
	dst[offset] = generateTOC(uint8(config), stereo, 3)
	offset++

	countByte := byte(0x01)
	if paddingAmount != 0 {
		countByte |= 0x40
	}
	dst[offset] = countByte
	offset++

	if paddingAmount != 0 {
		offset += writePaddingLength(dst[offset:], paddingAmount)
	}

	copy(dst[offset:], frameData)
	offset += len(frameData)

	if extLen > 0 {
		pos := extBegin
		for i, ext := range extensions {
			var err error
			pos, err = writePacketExtension(dst, pos, ext.ID, ext.Data, i == len(extensions)-1)
			if err != nil {
				return 0, err
			}
		}
	}
	for i := onesBegin; i < onesEnd; i++ {
		dst[i] = 0x01
	}
	if withPadding && extLen == 0 {
		for i := offset; i < need; i++ {
			dst[i] = 0
		}
	}

	return need, nil
}

// BuildPacket creates a complete Opus packet from encoded frame data.
// Uses frame code 0 (single frame).
func BuildPacket(frameData []byte, mode types.Mode, bandwidth types.Bandwidth, frameSize int, stereo bool) ([]byte, error) {
	config := configFromParams(mode, bandwidth, frameSize)
	if config < 0 {
		return nil, ErrInvalidConfig
	}

	toc := generateTOC(uint8(config), stereo, 0)

	// Packet = TOC + frame data
	packet := make([]byte, 1+len(frameData))
	packet[0] = toc
	copy(packet[1:], frameData)

	return packet, nil
}

// buildMultiFramePacketInto writes the same packet bytes as BuildMultiFramePacket
// into dst, returning the number of bytes written. It mirrors BuildMultiFramePacket's
// frame-code selection (code 0 for a single frame, codes 1/2 for two frames, code 3
// otherwise) so the emitted bytes are identical, but takes a caller-owned buffer to
// avoid a per-packet allocation on the long-packet hot path.
func buildMultiFramePacketInto(dst []byte, frames [][]byte, mode types.Mode, bandwidth types.Bandwidth, frameSize int, stereo bool, vbr bool) (int, error) {
	if len(frames) == 0 || len(frames) > 48 {
		return 0, ErrInvalidFrameCount
	}

	config := configFromParams(mode, bandwidth, frameSize)
	if config < 0 {
		return 0, ErrInvalidConfig
	}

	if len(frames) == 1 {
		return BuildPacketInto(dst, frames[0], mode, bandwidth, frameSize, stereo)
	}
	if len(frames) == 2 {
		if len(frames[0]) == len(frames[1]) {
			need := 1 + len(frames[0]) + len(frames[1])
			if len(dst) < need {
				return 0, ErrInvalidConfig
			}
			dst[0] = generateTOC(uint8(config), stereo, 1)
			copy(dst[1:], frames[0])
			copy(dst[1+len(frames[0]):], frames[1])
			return need, nil
		}
		headerSize := 1 + frameLengthBytes(len(frames[0]))
		need := headerSize + len(frames[0]) + len(frames[1])
		if len(dst) < need {
			return 0, ErrInvalidConfig
		}
		dst[0] = generateTOC(uint8(config), stereo, 2)
		offset := 1
		offset += writeFrameLength(dst[offset:], len(frames[0]))
		copy(dst[offset:], frames[0])
		offset += len(frames[0])
		copy(dst[offset:], frames[1])
		return need, nil
	}

	var countByte byte
	if vbr {
		countByte |= 0x80
	}
	countByte |= byte(len(frames) & 0x3F)

	headerSize := 2
	if vbr {
		for i := 0; i < len(frames)-1; i++ {
			headerSize += frameLengthBytes(len(frames[i]))
		}
	}

	totalFrameSize := 0
	for _, f := range frames {
		totalFrameSize += len(f)
	}

	need := headerSize + totalFrameSize
	if len(dst) < need {
		return 0, ErrInvalidConfig
	}
	dst[0] = generateTOC(uint8(config), stereo, 3)
	dst[1] = countByte

	offset := 2
	if vbr {
		for i := 0; i < len(frames)-1; i++ {
			offset += writeFrameLength(dst[offset:], len(frames[i]))
		}
	}
	for _, f := range frames {
		copy(dst[offset:], f)
		offset += len(f)
	}

	return need, nil
}

// BuildMultiFramePacket creates a packet with multiple frames.
// frames: slice of encoded frame data
// vbr: true for variable bitrate (different frame sizes), false for CBR
func BuildMultiFramePacket(frames [][]byte, mode types.Mode, bandwidth types.Bandwidth, frameSize int, stereo bool, vbr bool) ([]byte, error) {
	if len(frames) == 0 || len(frames) > 48 {
		return nil, ErrInvalidFrameCount
	}

	config := configFromParams(mode, bandwidth, frameSize)
	if config < 0 {
		return nil, ErrInvalidConfig
	}

	if len(frames) == 1 {
		return BuildPacket(frames[0], mode, bandwidth, frameSize, stereo)
	}
	if len(frames) == 2 {
		if len(frames[0]) == len(frames[1]) {
			toc := generateTOC(uint8(config), stereo, 1)
			packet := make([]byte, 1+len(frames[0])+len(frames[1]))
			packet[0] = toc
			copy(packet[1:], frames[0])
			copy(packet[1+len(frames[0]):], frames[1])
			return packet, nil
		}
		toc := generateTOC(uint8(config), stereo, 2)
		headerSize := 1 + frameLengthBytes(len(frames[0]))
		packet := make([]byte, headerSize+len(frames[0])+len(frames[1]))
		packet[0] = toc
		offset := 1
		offset += writeFrameLength(packet[offset:], len(frames[0]))
		copy(packet[offset:], frames[0])
		offset += len(frames[0])
		copy(packet[offset:], frames[1])
		return packet, nil
	}

	toc := generateTOC(uint8(config), stereo, 3) // Code 3

	// Frame count byte: VBR flag | padding flag | count
	var countByte byte
	if vbr {
		countByte |= 0x80 // VBR bit
	}
	countByte |= byte(len(frames) & 0x3F)

	// Calculate total size
	headerSize := 2 // TOC + count
	if vbr {
		// Add frame length bytes for all but last frame
		for i := 0; i < len(frames)-1; i++ {
			headerSize += frameLengthBytes(len(frames[i]))
		}
	}

	totalFrameSize := 0
	for _, f := range frames {
		totalFrameSize += len(f)
	}

	packet := make([]byte, headerSize+totalFrameSize)
	packet[0] = toc
	packet[1] = countByte

	offset := 2
	if vbr {
		// Write frame lengths for all but last
		for i := 0; i < len(frames)-1; i++ {
			n := writeFrameLength(packet[offset:], len(frames[i]))
			offset += n
		}
	}

	// Write frame data
	for _, f := range frames {
		copy(packet[offset:], f)
		offset += len(f)
	}

	return packet, nil
}

// frameLengthBytes returns number of bytes needed to encode frame length.
func frameLengthBytes(length int) int {
	if length < 252 {
		return 1
	}
	return 2
}

// writeFrameLength writes frame length at offset, returns bytes written.
func writeFrameLength(dst []byte, length int) int {
	if length < 252 {
		dst[0] = byte(length)
		return 1
	}
	// Two-byte encoding per RFC 6716 Section 3.2.1:
	// For lengths >= 252, use two bytes where:
	// length = 4*secondByte + firstByte
	// Solve for firstByte and secondByte:
	// firstByte = 252 + (length % 4)  (must be >= 252, so add base)
	// secondByte = (length - firstByte) / 4 = (length - 252 - (length % 4)) / 4
	//            = (length - 252) / 4 (integer division handles the remainder)
	dst[0] = byte(252 + (length % 4))
	dst[1] = byte((length - 252) / 4)
	return 2
}

func packetExtensionLength(id int, data []byte, last bool) int {
	return packetExtensionDataLength(id, len(data), last)
}

func packetExtensionDataLength(id int, dataLen int, last bool) int {
	if id < 3 || id > 127 {
		return 0
	}
	if id < 32 {
		return 1 + dataLen
	}
	if last {
		return 1 + dataLen
	}
	return 2 + dataLen/255 + dataLen
}

func writePacketExtensionPayload(dst []byte, pos int, id int, data []byte, last bool) (int, error) {
	if id < 3 || id > 127 {
		return 0, ErrInvalidConfig
	}
	if id < 32 {
		if len(data) > 1 {
			return 0, ErrInvalidConfig
		}
		if len(dst)-pos < len(data) {
			return 0, ErrInvalidConfig
		}
		copy(dst[pos:], data)
		return pos + len(data), nil
	}

	lengthBytes := 1 + len(data)/255
	if last {
		lengthBytes = 0
	}
	if len(dst)-pos < lengthBytes+len(data) {
		return 0, ErrInvalidConfig
	}
	if !last {
		for j := 0; j < len(data)/255; j++ {
			dst[pos] = 255
			pos++
		}
		dst[pos] = byte(len(data) % 255)
		pos++
	}
	copy(dst[pos:], data)
	return pos + len(data), nil
}

func writePacketExtension(dst []byte, pos int, id int, data []byte, last bool) (int, error) {
	if id < 3 || id > 127 {
		return 0, ErrInvalidConfig
	}
	if len(dst)-pos < 1 {
		return 0, ErrInvalidConfig
	}

	lFlag := 0
	if id < 32 {
		lFlag = len(data)
		if lFlag < 0 || lFlag > 1 {
			return 0, ErrInvalidConfig
		}
	} else if !last {
		lFlag = 1
	}
	dst[pos] = byte((id << 1) | lFlag)
	pos++
	return writePacketExtensionPayload(dst, pos, id, data, last)
}

func generatePacketExtensions(dst []byte, length int, extensions []packetExtension, nbFrames int, pad bool) (int, error) {
	if nbFrames < 0 || nbFrames > 48 || length < 0 {
		return 0, ErrInvalidConfig
	}
	if dst != nil && len(dst) < length {
		return 0, ErrInvalidConfig
	}

	var frameMinIdxBak, frameMaxIdxBak, frameRepeatIdxBak [48]int
	frameMinIdx := frameMinIdxBak[:nbFrames]
	frameMaxIdx := frameMaxIdxBak[:nbFrames]
	frameRepeatIdx := frameRepeatIdxBak[:nbFrames]
	for f := range nbFrames {
		frameMinIdx[f] = len(extensions)
	}

	for i, ext := range extensions {
		if ext.Frame < 0 || ext.Frame >= nbFrames || ext.ID < 3 || ext.ID > 127 {
			return 0, ErrInvalidConfig
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
				if diff <= 0 || length-pos < 2 {
					return 0, ErrInvalidConfig
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
				pos, err = writePacketExtension(dst, pos, extensions[i].ID, extensions[i].Data, last)
				if err != nil {
					return 0, err
				}
			} else {
				size := packetExtensionDataLength(extensions[i].ID, len(extensions[i].Data), last)
				if length-pos < size {
					return 0, ErrInvalidConfig
				}
				pos += size
			}
			written++

			if repeatCount > 0 && frameRepeatIdx[f] == i {
				nbRepeated := repeatCount * (nbFrames - (f + 1))
				last := written+nbRepeated == len(extensions) || (lastLongIdx < 0 && i+1 >= frameMaxIdx[f])
				if length-pos < 1 {
					return 0, ErrInvalidConfig
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
							pos, err = writePacketExtensionPayload(dst, pos, extensions[j].ID, extensions[j].Data, last && j == lastLongIdx)
							if err != nil {
								return 0, err
							}
						} else {
							size := len(extensions[j].Data)
							if extensions[j].ID < 32 {
								if size > 1 {
									return 0, ErrInvalidConfig
								}
							} else if !last || j != lastLongIdx {
								size += 1 + len(extensions[j].Data)/255
							}
							if length-pos < size {
								return 0, ErrInvalidConfig
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
		return 0, ErrInvalidConfig
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

func paddingLengthBytes(extra int) int {
	if extra <= 0 {
		return 0
	}
	return 1 + (extra-1)/255
}

func writePaddingLength(dst []byte, extra int) int {
	if extra <= 0 {
		return 0
	}
	n := 0
	remaining := extra
	for remaining > 255 {
		dst[n] = 255
		n++
		remaining -= 255
	}
	dst[n] = byte(remaining - 1)
	return n + 1
}

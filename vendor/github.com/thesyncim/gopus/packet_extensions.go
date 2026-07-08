package gopus

type packetExtensionData struct {
	ID    int
	Frame int
	Data  []byte
}

const qextPacketExtensionID = 124

type packetExtensionIterator struct {
	data             []byte
	currPos          int
	repeatPos        int
	lastLongPos      int
	srcPos           int
	currLen          int
	repeatLen        int
	srcLen           int
	trailingShortLen int
	nbFrames         int
	frameMax         int
	currFrame        int
	repeatFrame      int
	repeatL          byte
}

func initPacketExtensionIterator(iter *packetExtensionIterator, data []byte, nbFrames int) {
	if nbFrames < 0 {
		nbFrames = 0
	}
	if nbFrames > maxRepacketizerFrames {
		nbFrames = maxRepacketizerFrames
	}
	*iter = packetExtensionIterator{
		data:        data,
		currLen:     len(data),
		nbFrames:    nbFrames,
		frameMax:    nbFrames,
		lastLongPos: -1,
	}
}

func skipPacketExtensionPayload(data []byte, pos, length int, idByte int, trailingShortLen int) (newPos, newLen, headerSize int, err error) {
	id := idByte >> 1
	lFlag := idByte & 1
	headerSize = 0

	switch {
	case (id == 0 && lFlag == 1) || id == 2:
		return pos, length, headerSize, nil
	case id > 0 && id < 32:
		if length < lFlag {
			return 0, 0, 0, ErrInvalidPacket
		}
		pos += lFlag
		length -= lFlag
		return pos, length, headerSize, nil
	default:
		if lFlag == 0 {
			if length < trailingShortLen {
				return 0, 0, 0, ErrInvalidPacket
			}
			pos += length - trailingShortLen
			length = trailingShortLen
			return pos, length, headerSize, nil
		}

		bytes := 0
		for {
			if length < 1 || pos >= len(data) {
				return 0, 0, 0, ErrInvalidPacket
			}
			lacing := int(data[pos])
			pos++
			bytes += lacing
			headerSize++
			length -= lacing + 1
			if lacing != 255 {
				break
			}
		}
		if length < 0 {
			return 0, 0, 0, ErrInvalidPacket
		}
		pos += bytes
		return pos, length, headerSize, nil
	}
}

func skipPacketExtension(data []byte, pos, length int) (newPos, newLen, headerSize int, err error) {
	if length == 0 {
		return pos, 0, 0, nil
	}
	if length < 1 || pos >= len(data) {
		return 0, 0, 0, ErrInvalidPacket
	}
	idByte := int(data[pos])
	pos++
	length--
	newPos, newLen, headerSize, err = skipPacketExtensionPayload(data, pos, length, idByte, 0)
	if err != nil {
		return 0, 0, 0, err
	}
	return newPos, newLen, headerSize + 1, nil
}

func (iter *packetExtensionIterator) nextRepeat(ext *packetExtensionData) (bool, error) {
	for ; iter.repeatFrame < iter.nbFrames; iter.repeatFrame++ {
		for iter.srcLen > 0 {
			repeatIDByte := int(iter.data[iter.srcPos])
			newSrcPos, newSrcLen, _, err := skipPacketExtension(iter.data, iter.srcPos, iter.srcLen)
			if err != nil {
				return false, err
			}
			iter.srcPos = newSrcPos
			iter.srcLen = newSrcLen

			if repeatIDByte <= 3 {
				continue
			}
			if iter.repeatL == 0 && iter.repeatFrame+1 >= iter.nbFrames && iter.srcPos == iter.lastLongPos {
				repeatIDByte &^= 1
			}

			currData0 := iter.currPos
			newCurrPos, newCurrLen, headerSize, err := skipPacketExtensionPayload(
				iter.data,
				iter.currPos,
				iter.currLen,
				repeatIDByte,
				iter.trailingShortLen,
			)
			if err != nil {
				return false, err
			}
			iter.currPos = newCurrPos
			iter.currLen = newCurrLen

			if iter.repeatFrame >= iter.frameMax {
				continue
			}
			if ext != nil {
				*ext = packetExtensionData{
					ID:    repeatIDByte >> 1,
					Frame: iter.repeatFrame,
					Data:  iter.data[currData0+headerSize : iter.currPos],
				}
			}
			return true, nil
		}
		iter.srcPos = iter.repeatPos
		iter.srcLen = iter.repeatLen
	}

	iter.repeatPos = iter.currPos
	iter.lastLongPos = -1
	if iter.repeatL == 0 {
		iter.currFrame++
		if iter.currFrame >= iter.nbFrames {
			iter.currLen = 0
		}
	}
	iter.repeatFrame = 0
	return false, nil
}

func (iter *packetExtensionIterator) next(ext *packetExtensionData) (bool, error) {
	if iter.repeatFrame > 0 {
		ok, err := iter.nextRepeat(ext)
		if ok || err != nil {
			return ok, err
		}
	}
	if iter.currFrame >= iter.frameMax {
		return false, nil
	}

	for iter.currLen > 0 {
		currData0 := iter.currPos
		idByte := int(iter.data[currData0])
		id := idByte >> 1
		lFlag := idByte & 1

		newCurrPos, newCurrLen, headerSize, err := skipPacketExtension(iter.data, iter.currPos, iter.currLen)
		if err != nil {
			return false, err
		}
		iter.currPos = newCurrPos
		iter.currLen = newCurrLen

		switch {
		case id == 1:
			if lFlag == 0 {
				iter.currFrame++
			} else {
				if currData0+1 >= len(iter.data) {
					return false, ErrInvalidPacket
				}
				diff := int(iter.data[currData0+1])
				if diff == 0 {
					continue
				}
				iter.currFrame += diff
			}
			if iter.currFrame >= iter.nbFrames {
				return false, ErrInvalidPacket
			}
			if iter.currFrame >= iter.frameMax {
				iter.currLen = 0
			}
			iter.repeatPos = iter.currPos
			iter.lastLongPos = -1
			iter.trailingShortLen = 0
		case id == 2:
			iter.repeatL = byte(lFlag)
			iter.repeatFrame = iter.currFrame + 1
			iter.repeatLen = currData0 - iter.repeatPos
			iter.srcPos = iter.repeatPos
			iter.srcLen = iter.repeatLen
			ok, err := iter.nextRepeat(ext)
			if ok || err != nil {
				return ok, err
			}
		case id > 2:
			if id >= 32 {
				iter.lastLongPos = iter.currPos
				iter.trailingShortLen = 0
			} else {
				iter.trailingShortLen += lFlag
			}
			if ext != nil {
				*ext = packetExtensionData{
					ID:    id,
					Frame: iter.currFrame,
					Data:  iter.data[currData0+headerSize : iter.currPos],
				}
			}
			return true, nil
		}
	}

	return false, nil
}

func findPacketExtension(data []byte, nbFrames, id int) (packetExtensionData, bool, error) {
	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, data, nbFrames)
	for {
		var ext packetExtensionData
		ok, err := iter.next(&ext)
		if err != nil {
			return packetExtensionData{}, false, err
		}
		if !ok {
			return packetExtensionData{}, false, nil
		}
		if ext.ID == id {
			return ext, true, nil
		}
	}
}

func countPacketExtensions(data []byte, nbFrames int) (int, error) {
	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, data, nbFrames)
	count := 0
	for {
		ok, err := iter.next(nil)
		if err != nil {
			return 0, err
		}
		if !ok {
			return count, nil
		}
		count++
	}
}

func countPacketExtensionsByFrame(data []byte, nbFrames int, counts []int) (int, error) {
	if len(counts) < nbFrames {
		return 0, ErrBufferTooSmall
	}
	clear(counts[:nbFrames])

	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, data, nbFrames)
	count := 0
	for {
		var ext packetExtensionData
		ok, err := iter.next(&ext)
		if err != nil {
			return 0, err
		}
		if !ok {
			return count, nil
		}
		counts[ext.Frame]++
		count++
	}
}

func parsePacketExtensions(data []byte, nbFrames int, extensions []packetExtensionData) (int, error) {
	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, data, nbFrames)
	count := 0
	for {
		var ext packetExtensionData
		ok, err := iter.next(&ext)
		if err != nil {
			return 0, err
		}
		if !ok {
			return count, nil
		}
		if count >= len(extensions) {
			return 0, ErrBufferTooSmall
		}
		extensions[count] = ext
		count++
	}
}

func parsePacketExtensionsFrameOrder(data []byte, nbFrames int, frameCounts []int, extensions []packetExtensionData) (int, error) {
	if len(frameCounts) < nbFrames {
		return 0, ErrBufferTooSmall
	}

	var cumul [maxRepacketizerFrames + 1]int
	prevTotal := 0
	for i := range nbFrames {
		total := frameCounts[i] + prevTotal
		cumul[i] = prevTotal
		prevTotal = total
	}
	cumul[nbFrames] = prevTotal

	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, data, nbFrames)

	count := 0
	for {
		var ext packetExtensionData
		ok, err := iter.next(&ext)
		if err != nil {
			return 0, err
		}
		if !ok {
			return count, nil
		}
		idx := cumul[ext.Frame]
		cumul[ext.Frame]++
		if idx >= len(extensions) {
			return 0, ErrBufferTooSmall
		}
		extensions[idx] = ext
		count++
	}
}

func writePacketExtensionPayload(dst []byte, pos int, ext packetExtensionData, last bool) (int, error) {
	if ext.ID < 3 || ext.ID > 127 {
		return 0, ErrInvalidArgument
	}

	if ext.ID < 32 {
		if len(ext.Data) > 1 {
			return 0, ErrInvalidArgument
		}
		if len(dst)-pos < len(ext.Data) {
			return 0, ErrBufferTooSmall
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
		return 0, ErrBufferTooSmall
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
		return 0, ErrInvalidArgument
	}
	if len(dst)-pos < 1 {
		return 0, ErrBufferTooSmall
	}

	lFlag := 0
	if ext.ID < 32 {
		lFlag = len(ext.Data)
		if lFlag < 0 || lFlag > 1 {
			return 0, ErrInvalidArgument
		}
	} else if !last {
		lFlag = 1
	}

	dst[pos] = byte((ext.ID << 1) | lFlag)
	pos++
	return writePacketExtensionPayload(dst, pos, ext, last)
}

func generatePacketExtensions(dst []byte, length int, extensions []packetExtensionData, nbFrames int, pad bool) (int, error) {
	if nbFrames < 0 || nbFrames > maxRepacketizerFrames {
		return 0, ErrInvalidArgument
	}
	if length < 0 {
		return 0, ErrInvalidArgument
	}
	if dst != nil && len(dst) < length {
		return 0, ErrBufferTooSmall
	}

	frameMinIdx := make([]int, nbFrames)
	frameMaxIdx := make([]int, nbFrames)
	frameRepeatIdx := make([]int, nbFrames)
	for f := range nbFrames {
		frameMinIdx[f] = len(extensions)
	}

	for i, ext := range extensions {
		if ext.Frame < 0 || ext.Frame >= nbFrames {
			return 0, ErrInvalidArgument
		}
		if ext.ID < 3 || ext.ID > 127 {
			return 0, ErrInvalidArgument
		}
		if ext.Frame < nbFrames {
			if i < frameMinIdx[ext.Frame] {
				frameMinIdx[ext.Frame] = i
			}
			if i+1 > frameMaxIdx[ext.Frame] {
				frameMaxIdx[ext.Frame] = i + 1
			}
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
					return 0, ErrBufferTooSmall
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
				// Sizing-only path.
				size := 1
				if extensions[i].ID < 32 {
					if len(extensions[i].Data) > 1 {
						return 0, ErrInvalidArgument
					}
					size += len(extensions[i].Data)
				} else {
					if !last {
						size += 1 + len(extensions[i].Data)/255
					}
					size += len(extensions[i].Data)
				}
				if length-pos < size {
					return 0, ErrBufferTooSmall
				}
				pos += size
			}
			written++

			if repeatCount > 0 && frameRepeatIdx[f] == i {
				nbRepeated := repeatCount * (nbFrames - (f + 1))
				last := written+nbRepeated == len(extensions) || (lastLongIdx < 0 && i+1 >= frameMaxIdx[f])
				if length-pos < 1 {
					return 0, ErrBufferTooSmall
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
									return 0, ErrInvalidArgument
								}
							} else if !last || j != lastLongIdx {
								size += 1 + len(extensions[j].Data)/255
							}
							if length-pos < size {
								return 0, ErrBufferTooSmall
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

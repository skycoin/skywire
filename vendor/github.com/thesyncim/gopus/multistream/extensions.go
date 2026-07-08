package multistream

type packetExtensionData struct {
	ID    int
	Frame int
	Data  []byte
}

const (
	maxPacketExtensionFrames = 48
	qextPacketExtensionID    = 124
)

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
	if nbFrames > maxPacketExtensionFrames {
		nbFrames = maxPacketExtensionFrames
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

	switch {
	case (id == 0 && lFlag == 1) || id == 2:
		return pos, length, 0, nil
	case id > 0 && id < 32:
		if length < lFlag {
			return 0, 0, 0, ErrInvalidPacket
		}
		pos += lFlag
		length -= lFlag
		return pos, length, 0, nil
	default:
		if lFlag == 0 {
			if length < trailingShortLen {
				return 0, 0, 0, ErrInvalidPacket
			}
			pos += length - trailingShortLen
			length = trailingShortLen
			return pos, length, 0, nil
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
			return iter.nextRepeat(ext)
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

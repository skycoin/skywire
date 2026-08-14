package vt

import "unicode/utf16"

// utf32ToString converts UTF32 codepoints to a Go string.
func utf32ToString(data []uint32, start, end int) string {
	runes := make([]rune, 0, end-start)
	for i := start; i < end; i++ {
		runes = append(runes, rune(data[i])) // #nosec G115 -- UTF-16 code units, at most 0xFFFF by construction
	}
	return string(runes)
}

// StringToUtf32 decodes UTF16-oriented JS-style strings into UTF32
// codepoints. For Go strings (UTF-8) use Utf8ToUtf32 or DecodeString.
type StringToUtf32 struct {
	interim rune
}

// Clear resets the decoder.
func (s *StringToUtf32) Clear() { s.interim = 0 }

// Decode writes codepoints of input into target, returning the count.
func (s *StringToUtf32) Decode(input []uint16, target []uint32) int {
	size := 0
	startPos := 0
	if s.interim != 0 && len(input) > 0 {
		second := input[startPos]
		startPos++
		if second >= 0xDC00 && second <= 0xDFFF {
			target[size] = uint32((s.interim-0xD800)*0x400) + uint32(second) - 0xDC00 + 0x10000 // #nosec G115 -- UTF-16 code units, at most 0xFFFF by construction
			size++
		} else {
			target[size] = uint32(s.interim) // #nosec G115 -- UTF-16 code units, at most 0xFFFF by construction
			size++
			target[size] = uint32(second)
			size++
		}
		s.interim = 0
	}
	for i := startPos; i < len(input); i++ {
		code := input[i]
		if code >= 0xD800 && code <= 0xDBFF {
			i++
			if i >= len(input) {
				s.interim = rune(code)
				return size
			}
			second := input[i]
			if second >= 0xDC00 && second <= 0xDFFF {
				target[size] = uint32(utf16.DecodeRune(rune(code), rune(second))) // #nosec G115 -- UTF-16 code units, at most 0xFFFF by construction
				size++
			} else {
				target[size] = uint32(code)
				size++
				target[size] = uint32(second)
				size++
			}
			continue
		}
		if code == 0xFEFF { // BOM
			continue
		}
		target[size] = uint32(code)
		size++
	}
	return size
}

// Utf8ToUtf32 decodes UTF8 byte streams into UTF32 codepoints with
// support for partly transmitted sequences across chunks
// (the port of Utf8ToUtf32).
type Utf8ToUtf32 struct {
	interim [3]byte
}

// Clear resets the decoder to a clean state.
func (d *Utf8ToUtf32) Clear() {
	d.interim = [3]byte{}
}

// Decode decodes UTF8 bytes in input to UTF32 codepoints in target,
// returning the number of written codepoints. target must be at least
// len(input) long.
func (d *Utf8ToUtf32) Decode(input []byte, target []uint32) int {
	length := len(input)
	if length == 0 {
		return 0
	}

	size := 0
	var byte1, byte2, byte3, byte4 byte
	var codepoint uint32
	startPos := 0

	// handle leftover bytes
	if d.interim[0] != 0 {
		discardInterim := false
		cp := uint32(d.interim[0])
		switch {
		case cp&0xE0 == 0xC0:
			cp &= 0x1F
		case cp&0xF0 == 0xE0:
			cp &= 0x0F
		default:
			cp &= 0x07
		}
		// mirror the JS `while ((tmp = this.interim[++pos] & 0x3F) && pos < 4)`
		// loop, including its quirk of stopping on a zero payload
		pos := 0
		for {
			pos++
			if pos >= 3 {
				break // JS reads interim[3] as undefined → 0 → loop exit
			}
			tmp := d.interim[pos] & 0x3F
			if tmp == 0 {
				break
			}
			cp <<= 6
			cp |= uint32(tmp)
		}
		var typ int
		switch {
		case d.interim[0]&0xE0 == 0xC0:
			typ = 2
		case d.interim[0]&0xF0 == 0xE0:
			typ = 3
		default:
			typ = 4
		}
		missing := typ - pos
		for startPos < missing {
			if startPos >= length {
				return 0
			}
			tmp := input[startPos]
			startPos++
			if tmp&0xC0 != 0x80 {
				// wrong continuation, discard interim bytes completely
				startPos--
				discardInterim = true
				break
			}
			// JS writes interim[pos++] even at pos 3, where the typed
			// array silently drops the write
			if pos < 3 {
				d.interim[pos] = tmp
			}
			pos++
			cp <<= 6
			cp |= uint32(tmp & 0x3F)
		}
		if !discardInterim {
			switch typ {
			case 2:
				if cp < 0x80 {
					startPos--
				} else {
					target[size] = cp
					size++
				}
			case 3:
				if cp < 0x0800 || (cp >= 0xD800 && cp <= 0xDFFF) || cp == 0xFEFF {
					// illegal codepoint or BOM
				} else {
					target[size] = cp
					size++
				}
			default:
				if cp < 0x010000 || cp > 0x10FFFF {
					// illegal codepoint
				} else {
					target[size] = cp
					size++
				}
			}
		}
		d.interim = [3]byte{}
	}

	// loop through input
	fourStop := length - 4
	i := startPos
	for i < length {
		// ASCII shortcut, 4 bytes at a time
		for i < fourStop {
			byte1 = input[i]
			if byte1&0x80 != 0 {
				break
			}
			byte2 = input[i+1]
			if byte2&0x80 != 0 {
				break
			}
			byte3 = input[i+2]
			if byte3&0x80 != 0 {
				break
			}
			byte4 = input[i+3]
			if byte4&0x80 != 0 {
				break
			}
			target[size] = uint32(byte1)
			target[size+1] = uint32(byte2)
			target[size+2] = uint32(byte3)
			target[size+3] = uint32(byte4)
			size += 4
			i += 4
		}

		if i >= length {
			break
		}
		byte1 = input[i]
		i++

		switch {
		case byte1 < 0x80: // 1 byte
			target[size] = uint32(byte1)
			size++
		case byte1&0xE0 == 0xC0: // 2 bytes
			if i >= length {
				d.interim[0] = byte1
				return size
			}
			byte2 = input[i]
			i++
			if byte2&0xC0 != 0x80 {
				i--
				continue
			}
			codepoint = uint32(byte1&0x1F)<<6 | uint32(byte2&0x3F)
			if codepoint < 0x80 {
				i--
				continue
			}
			target[size] = codepoint
			size++
		case byte1&0xF0 == 0xE0: // 3 bytes
			if i >= length {
				d.interim[0] = byte1
				return size
			}
			byte2 = input[i]
			i++
			if byte2&0xC0 != 0x80 {
				i--
				continue
			}
			if i >= length {
				d.interim[0] = byte1
				d.interim[1] = byte2
				return size
			}
			byte3 = input[i]
			i++
			if byte3&0xC0 != 0x80 {
				i--
				continue
			}
			codepoint = uint32(byte1&0x0F)<<12 | uint32(byte2&0x3F)<<6 | uint32(byte3&0x3F)
			if codepoint < 0x0800 || (codepoint >= 0xD800 && codepoint <= 0xDFFF) || codepoint == 0xFEFF {
				continue
			}
			target[size] = codepoint
			size++
		case byte1&0xF8 == 0xF0: // 4 bytes
			if i >= length {
				d.interim[0] = byte1
				return size
			}
			byte2 = input[i]
			i++
			if byte2&0xC0 != 0x80 {
				i--
				continue
			}
			if i >= length {
				d.interim[0] = byte1
				d.interim[1] = byte2
				return size
			}
			byte3 = input[i]
			i++
			if byte3&0xC0 != 0x80 {
				i--
				continue
			}
			if i >= length {
				d.interim[0] = byte1
				d.interim[1] = byte2
				d.interim[2] = byte3
				return size
			}
			byte4 = input[i]
			i++
			if byte4&0xC0 != 0x80 {
				i--
				continue
			}
			codepoint = uint32(byte1&0x07)<<18 | uint32(byte2&0x3F)<<12 | uint32(byte3&0x3F)<<6 | uint32(byte4&0x3F)
			if codepoint < 0x010000 || codepoint > 0x10FFFF {
				continue
			}
			target[size] = codepoint
			size++
		default:
			// illegal byte, just skip
		}
	}
	return size
}

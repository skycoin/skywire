package ansifilter

// This file ports the BIN, XBIN and Tundra ANSI art decoders. The XBIN and TND
// routines follow the AnsiLove implementations (https://github.com/ansilove/),
// as the C++ original does.

// streamIsXBIN reports whether the input begins with the XBIN magic. Like the
// C++ original it only inspects seekable inputs, so piped data is never
// treated as XBIN.
func (g *Generator) streamIsXBIN() bool {
	if g.seeker == nil {
		return false
	}
	head, err := g.in.Peek(4)
	if err != nil {
		return false
	}
	return string(head) == "XBIN"
}

// streamIsTundra reports whether the input begins with the Tundra magic.
func (g *Generator) streamIsTundra() bool {
	if g.seeker == nil {
		return false
	}
	head, err := g.in.Peek(9)
	if err != nil {
		return false
	}
	return string(head) == "\x18TUNDRA24"
}

// getByte reads one byte, returning -1 at end of input, like istream::get.
func (g *Generator) getByte() int {
	b, err := g.in.ReadByte()
	if err != nil {
		return -1
	}
	return int(b)
}

// readN reads exactly n bytes, reporting false if the input ends first.
func (g *Generator) readN(n int) ([]byte, bool) {
	buf := make([]byte, n)
	total := 0
	for total < n {
		got, err := g.in.Read(buf[total:])
		total += got
		if err != nil {
			return buf, total == n
		}
	}
	return buf, true
}

// parseBinFile decodes a flat BIN image: alternating character and attribute
// bytes across a fixed-width console.
func (g *Generator) parseBinFile() {
	g.allocateTermBuffer()
	count := 0
	for {
		buffer, ok := g.readN(2)
		if !ok {
			break
		}
		cur := int(buffer[0]) & 0xff
		next := int(buffer[1]) & 0xff

		colBg := (next & 240) >> 4
		colFg := next & 15
		if colBg > 8 {
			colBg -= 8
		}

		g.elementStyle.setFgColorStr(rgb2html(g.workingPalette[colFg]))
		g.elementStyle.setBgColorStr(rgb2html(g.workingPalette[colBg]))
		g.elementStyle.setBold(cur >= 0x20 && cur <= 0x7a)

		if g.curX < g.asciiArtWidth && g.curY < g.asciiArtHeight {
			idx := g.curX + g.curY*g.asciiArtWidth
			if idx < uint32(len(g.termBuffer)) {
				g.termBuffer[idx].c = byte(cur)
				g.termBuffer[idx].style = g.elementStyle
			}
			g.curX++
		}
		if uint32(count)%g.asciiArtWidth == 0 {
			g.curY++
			if g.maxY < g.curY && g.curY < g.asciiArtHeight {
				g.maxY = g.curY
			}
			g.curX = 0
		}
		count += 2
	}
}

// parseXBinFile decodes an XBIN image, honoring its optional palette, font
// block and RLE compression.
func (g *Generator) parseXBinFile() {
	header, ok := g.readN(11)
	if !ok {
		return
	}

	// The original masks the assembled 16 bit values with 0xff, so only the
	// low byte survives; reproduce that exactly.
	g.asciiArtWidth = uint32(0xff & ((int(int8(header[6])) << 8) + int(int8(header[5]))))
	g.asciiArtHeight = uint32(0xff & ((int(int8(header[8])) << 8) + int(int8(header[7]))))
	fontSize := int(int8(header[9]))
	flags := int(int8(header[10]))

	g.allocateTermBuffer()

	if flags&1 == 1 {
		if palette, ok := g.readN(48); ok {
			// Override the default palette with the embedded one.
			for loop := 0; loop < 16; loop++ {
				index := loop * 3
				g.workingPalette[loop][0] = byte(int(int8(palette[index]))<<2 | int(int8(palette[index]))>>4)
				g.workingPalette[loop][1] = byte(int(int8(palette[index+1]))<<2 | int(int8(palette[index+1]))>>4)
				g.workingPalette[loop][2] = byte(int(int8(palette[index+2]))<<2 | int(int8(palette[index+2]))>>4)
			}
		}
	}

	// Skip the embedded font.
	if flags&2 == 2 {
		numchars := 256
		if flags&0x10 != 0 {
			numchars = 512
		}
		_, _ = g.in.Discard(fontSize * numchars)
	}

	if flags&4 == 4 {
		for g.curY < g.asciiArtHeight {
			c := g.getByte()
			if c < 0 {
				break
			}
			compression := c & 0xC0
			cnt := (c & 0x3F) + 1

			cur := -1
			attr := -1

			for cnt > 0 {
				cnt--
				switch compression {
				case 0: // no compression
					cur = g.getByte()
					attr = g.getByte()
				case 0x40: // character run
					if cur == -1 {
						cur = g.getByte()
					}
					attr = g.getByte()
				case 0x80: // attribute run
					if attr == -1 {
						attr = g.getByte()
					}
					cur = g.getByte()
				default: // both
					if cur == -1 {
						cur = g.getByte()
					}
					if attr == -1 {
						attr = g.getByte()
					}
				}

				colBg := (attr & 240) >> 4
				colFg := attr & 15
				if colBg > 8 {
					colBg -= 8
				}
				if colFg < 0 || colFg > 15 || colBg < 0 || colBg > 15 {
					return
				}

				g.elementStyle.setFgColorStr(rgb2html(g.workingPalette[colFg]))
				g.elementStyle.setBgColorStr(rgb2html(g.workingPalette[colBg]))
				g.elementStyle.setBold(cur >= 0x20 && cur <= 0x7a)

				if g.curX < g.asciiArtWidth && g.curY < g.asciiArtHeight {
					idx := g.curX + g.curY*g.asciiArtWidth
					if idx < uint32(len(g.termBuffer)) {
						g.termBuffer[idx].c = byte(cur)
						g.termBuffer[idx].style = g.elementStyle
					}
					g.curX++
				}
				if g.curX == g.asciiArtWidth {
					g.curX = 0
					g.curY++
					if g.maxY < g.curY && g.curY < g.asciiArtHeight {
						g.maxY = g.curY
					}
				}
			}
		}
	} else {
		// Flat BIN payload.
		g.parseBinFile()
	}
}

// parseTundraFile decodes a Tundra 24 bit color image.
func (g *Generator) parseTundraFile() {
	head, ok := g.readN(9)
	if !ok || string(head) != "\x18TUNDRA24" {
		return
	}

	g.asciiArtWidth = 80
	g.allocateTermBuffer()

	var fgRed, fgGreen, fgBlue int
	var bgRed, bgGreen, bgBlue int

	for {
		b, ok := g.readN(1)
		if !ok {
			break
		}
		if g.curX >= g.asciiArtWidth {
			g.curX = 0
			g.curY++
		}

		cur := int(b[0]) & 0xff

		if cur == 1 {
			buf, ok := g.readN(8)
			if !ok {
				break
			}
			// Each term is masked individually, so only the last byte of each
			// pair contributes. This mirrors the original expression.
			g.curY = uint32(((int(int8(buf[0])) << 24) & 0xff) + ((int(int8(buf[1])) << 16) & 0xff) +
				((int(int8(buf[2])) << 8) & 0xff) + (int(int8(buf[3])) & 0xff))
			g.curX = uint32(((int(int8(buf[4])) << 24) & 0xff) + ((int(int8(buf[5])) << 16) & 0xff) +
				((int(int8(buf[6])) << 8) & 0xff) + (int(int8(buf[7])) & 0xff))
		}

		if cur == 2 {
			buf, ok := g.readN(5)
			if !ok {
				break
			}
			fgRed = int(int8(buf[2]))
			fgGreen = int(int8(buf[3]))
			fgBlue = int(int8(buf[4]))
			cur = int(int8(buf[0]))
		}

		if cur == 4 {
			buf, ok := g.readN(5)
			if !ok {
				break
			}
			bgRed = int(int8(buf[2]))
			bgGreen = int(int8(buf[3]))
			bgBlue = int(int8(buf[4]))
			cur = int(int8(buf[0]))
		}

		if cur == 6 {
			buf, ok := g.readN(10)
			if !ok {
				break
			}
			fgRed = int(int8(buf[2]))
			fgGreen = int(int8(buf[3]))
			fgBlue = int(int8(buf[4]))
			bgRed = int(int8(buf[6]))
			bgGreen = int(int8(buf[7]))
			bgBlue = int(int8(buf[8]))
			cur = int(int8(buf[0]))
		}

		if cur != 1 && cur != 2 && cur != 4 && cur != 6 {
			g.elementStyle.setFgColorStr(rgb2htmlInts(fgRed, fgGreen, fgBlue))
			g.elementStyle.setBgColorStr(rgb2htmlInts(bgRed, bgGreen, bgBlue))

			idx := g.curX + g.curY*g.asciiArtWidth
			if idx < uint32(len(g.termBuffer)) {
				g.termBuffer[idx].style = g.elementStyle
				g.termBuffer[idx].c = byte(cur & 0xff)
			}
			g.curX++
		}
	}

	if g.curY < g.asciiArtHeight {
		g.maxY = g.curY
	} else {
		g.maxY = g.asciiArtHeight
	}
}

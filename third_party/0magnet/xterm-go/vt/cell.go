package vt

// CharData mirrors the legacy [attr, chars, width, code] tuple.
type CharData struct {
	Attr  uint32
	Chars string
	Width int
	Code  int
}

// CellData represents a single cell in the terminal buffer
// (port of CellData; embeds AttributeData).
type CellData struct {
	AttributeData
	Content      uint32
	CombinedData string
}

// NewCellData creates an empty cell.
func NewCellData() *CellData {
	return &CellData{AttributeData: AttributeData{Extended: NewExtendedAttrs()}}
}

// CellDataFromCharData creates a CellData from CharData.
func CellDataFromCharData(value CharData) *CellData {
	obj := NewCellData()
	obj.SetFromCharData(value)
	return obj
}

// IsCombined reports whether the cell contains a combined string.
func (c *CellData) IsCombined() bool { return c.Content&ContentIsCombinedMask != 0 }

// GetWidth returns the cell width.
func (c *CellData) GetWidth() int { return int(c.Content >> ContentWidthShift) }

// GetChars returns the string content of the cell.
func (c *CellData) GetChars() string {
	if c.Content&ContentIsCombinedMask != 0 {
		return c.CombinedData
	}
	if c.Content&ContentCodepointMask != 0 {
		return string(rune(c.Content & ContentCodepointMask))
	}
	return ""
}

// GetCode returns the UTF32 codepoint (for combined content the last
// UTF-16 unit, matching the original).
func (c *CellData) GetCode() int {
	if c.IsCombined() {
		return lastUTF16Unit(c.CombinedData)
	}
	return int(c.Content & ContentCodepointMask)
}

// lastUTF16Unit mirrors JS charCodeAt(length-1) on Go strings.
func lastUTF16Unit(s string) int {
	units := utf16Units(s)
	if len(units) == 0 {
		return 0
	}
	return int(units[len(units)-1])
}

// Utf16Units converts a string to UTF-16 code units (JS string
// semantics — used by the browser layer for textarea value math).
func Utf16Units(s string) []uint16 { return utf16Units(s) }

// Utf16ToString converts UTF-16 code units back to a string.
func Utf16ToString(units []uint16) string {
	var sb []rune
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) {
			second := units[i+1]
			if second >= 0xDC00 && second <= 0xDFFF {
				sb = append(sb, rune(u-0xD800)*0x400+rune(second-0xDC00)+0x10000)
				i++
				continue
			}
		}
		sb = append(sb, rune(u))
	}
	return string(sb)
}

func utf16Units(s string) []uint16 {
	var units []uint16
	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			units = append(units, uint16(r>>10)+0xD800, uint16(r&0x3FF)+0xDC00) // #nosec G115 -- UTF-16 code units and a 0-2 cell width
		} else {
			units = append(units, uint16(r)) // #nosec G115 -- UTF-16 code units and a 0-2 cell width
		}
	}
	return units
}

// SetFromCharData fills the cell from CharData.
func (c *CellData) SetFromCharData(value CharData) {
	c.Fg = value.Attr
	c.Bg = 0
	combined := false
	units := utf16Units(value.Chars)
	switch {
	case len(units) > 2:
		combined = true
	case len(units) == 2:
		code := units[0]
		// if the 2-unit string is a surrogate pair create a single codepoint,
		// everything else is combined
		if code >= 0xD800 && code <= 0xDBFF {
			second := units[1]
			if second >= 0xDC00 && second <= 0xDFFF {
				c.Content = (uint32(code)-0xD800)*0x400 + uint32(second) - 0xDC00 + 0x10000 |
					uint32(value.Width)<<ContentWidthShift // #nosec G115 -- UTF-16 code units and a 0-2 cell width
			} else {
				combined = true
			}
		} else {
			combined = true
		}
	case len(units) == 1:
		c.Content = uint32(units[0]) | uint32(value.Width)<<ContentWidthShift // #nosec G115 -- UTF-16 code units and a 0-2 cell width
	default:
		c.Content = uint32(value.Width) << ContentWidthShift // #nosec G115 -- UTF-16 code units and a 0-2 cell width
	}
	if combined {
		c.CombinedData = value.Chars
		c.Content = ContentIsCombinedMask | uint32(value.Width)<<ContentWidthShift // #nosec G115 -- UTF-16 code units and a 0-2 cell width
	}
}

// GetAsCharData returns the cell as CharData.
func (c *CellData) GetAsCharData() CharData {
	return CharData{Attr: c.Fg, Chars: c.GetChars(), Width: c.GetWidth(), Code: c.GetCode()}
}

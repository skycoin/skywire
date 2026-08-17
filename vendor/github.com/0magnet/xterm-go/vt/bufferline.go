package vt

// buffer memory layout per cell (3 uint32 slots):
//
//	| content: wcwidth(2) comb(1) codepoint(21) | FG | BG |
const cellSize = 3

const (
	cellContent = 0
	cellFG      = 1
	cellBG      = 2
)

// BufferLine is the typed-array based buffer line implementation.
type BufferLine struct {
	data          []uint32
	combined      map[int]string
	extendedAttrs map[int]*ExtendedAttrs
	Length        int
	IsWrapped     bool
}

// NewBufferLine creates a line with cols cells filled with fillCellData
// (nil = null cells).
func NewBufferLine(cols int, fillCellData *CellData, isWrapped bool) *BufferLine {
	line := &BufferLine{
		data:          make([]uint32, cols*cellSize),
		combined:      map[int]string{},
		extendedAttrs: map[int]*ExtendedAttrs{},
		Length:        cols,
		IsWrapped:     isWrapped,
	}
	cell := fillCellData
	if cell == nil {
		cell = CellDataFromCharData(CharData{Attr: 0, Chars: "", Width: NullCellWidth, Code: NullCellCode})
	}
	for i := 0; i < cols; i++ {
		line.SetCell(i, cell)
	}
	return line
}

// GetWidth returns the width of the cell at index.
func (l *BufferLine) GetWidth(index int) int {
	return int(l.data[index*cellSize+cellContent] >> ContentWidthShift)
}

// HasWidth reports whether the cell has width.
func (l *BufferLine) HasWidth(index int) uint32 {
	return l.data[index*cellSize+cellContent] & ContentWidthMask
}

// GetFg returns the FG component.
func (l *BufferLine) GetFg(index int) uint32 { return l.data[index*cellSize+cellFG] }

// GetBg returns the BG component.
func (l *BufferLine) GetBg(index int) uint32 { return l.data[index*cellSize+cellBG] }

// HasContent reports whether the cell contains any chars.
func (l *BufferLine) HasContent(index int) bool {
	return l.data[index*cellSize+cellContent]&ContentHasContentMask != 0
}

// GetCodePoint returns the codepoint (or last UTF-16 unit of combined
// content).
func (l *BufferLine) GetCodePoint(index int) int {
	content := l.data[index*cellSize+cellContent]
	if content&ContentIsCombinedMask != 0 {
		return lastUTF16Unit(l.combined[index])
	}
	return int(content & ContentCodepointMask)
}

// IsCombined reports whether the cell contains a combined string.
func (l *BufferLine) IsCombined(index int) bool {
	return l.data[index*cellSize+cellContent]&ContentIsCombinedMask != 0
}

// GetString returns the string content of the cell.
func (l *BufferLine) GetString(index int) string {
	content := l.data[index*cellSize+cellContent]
	if content&ContentIsCombinedMask != 0 {
		return l.combined[index]
	}
	if content&ContentCodepointMask != 0 {
		return string(rune(content & ContentCodepointMask))
	}
	return ""
}

// IsProtected reports the protected flag of the cell.
func (l *BufferLine) IsProtected(index int) bool {
	return l.data[index*cellSize+cellBG]&BgProtected != 0
}

// LoadCell loads the cell at index into cell (GC friendly accessor).
func (l *BufferLine) LoadCell(index int, cell *CellData) *CellData {
	start := index * cellSize
	cell.Content = l.data[start+cellContent]
	cell.Fg = l.data[start+cellFG]
	cell.Bg = l.data[start+cellBG]
	if cell.Content&ContentIsCombinedMask != 0 {
		cell.CombinedData = l.combined[index]
	}
	if cell.Bg&BgHasExtended != 0 {
		cell.Extended = l.extendedAttrs[index]
	}
	return cell
}

// SetCell stores cell data at index.
func (l *BufferLine) SetCell(index int, cell *CellData) {
	if cell.Content&ContentIsCombinedMask != 0 {
		l.combined[index] = cell.CombinedData
	}
	if cell.Bg&BgHasExtended != 0 {
		l.extendedAttrs[index] = cell.Extended
	}
	l.data[index*cellSize+cellContent] = cell.Content
	l.data[index*cellSize+cellFG] = cell.Fg
	l.data[index*cellSize+cellBG] = cell.Bg
}

// SetCellFromCodepoint sets cell data from the input handler fast path.
func (l *BufferLine) SetCellFromCodepoint(index int, codePoint uint32, width int, attrs *AttributeData) {
	if attrs.Bg&BgHasExtended != 0 {
		l.extendedAttrs[index] = attrs.Extended
	}
	l.data[index*cellSize+cellContent] = codePoint | uint32(width)<<ContentWidthShift // #nosec G115 -- cell width is 0-2 and codepoints are at most 0x10FFFF
	l.data[index*cellSize+cellFG] = attrs.Fg
	l.data[index*cellSize+cellBG] = attrs.Bg
}

// AddCodepointToCell combines a zero-width codepoint onto the cell.
func (l *BufferLine) AddCodepointToCell(index int, codePoint uint32, width int) {
	content := l.data[index*cellSize+cellContent]
	if content&ContentIsCombinedMask != 0 {
		// we already have a combined string, simply add
		l.combined[index] += string(rune(codePoint)) // #nosec G115 -- cell width is 0-2 and codepoints are at most 0x10FFFF
	} else {
		if content&ContentCodepointMask != 0 {
			// move current leading char + new one into combined string
			l.combined[index] = string(rune(content&ContentCodepointMask)) + string(rune(codePoint)) // #nosec G115 -- cell width is 0-2 and codepoints are at most 0x10FFFF
			content &= ^ContentCodepointMask
			content |= ContentIsCombinedMask
		} else {
			// should not happen - no data in the cell yet
			content = codePoint | 1<<ContentWidthShift
		}
	}
	if width != 0 {
		content &= ^ContentWidthMask
		content |= uint32(width) << ContentWidthShift // #nosec G115 -- cell width is 0-2 and codepoints are at most 0x10FFFF
	}
	l.data[index*cellSize+cellContent] = content
}

// InsertCells inserts n fill cells at pos, shifting the rest right.
func (l *BufferLine) InsertCells(pos, n int, fillCellData *CellData) {
	pos %= l.Length

	// handle fullwidth at pos: reset cell to the left if pos is second
	// cell of a wide char
	if pos > 0 && l.GetWidth(pos-1) == 2 {
		l.SetCellFromCodepoint(pos-1, 0, 1, &fillCellData.AttributeData)
	}

	if n < l.Length-pos {
		cell := NewCellData()
		for i := l.Length - pos - n - 1; i >= 0; i-- {
			l.SetCell(pos+n+i, l.LoadCell(pos+i, cell))
		}
		for i := 0; i < n; i++ {
			l.SetCell(pos+i, fillCellData)
		}
	} else {
		for i := pos; i < l.Length; i++ {
			l.SetCell(i, fillCellData)
		}
	}

	// handle fullwidth at line end: reset last cell if it is first cell
	// of a wide char
	if l.GetWidth(l.Length-1) == 2 {
		l.SetCellFromCodepoint(l.Length-1, 0, 1, &fillCellData.AttributeData)
	}
}

// DeleteCells deletes n cells at pos, filling from the right.
func (l *BufferLine) DeleteCells(pos, n int, fillCellData *CellData) {
	pos %= l.Length
	if n < l.Length-pos {
		cell := NewCellData()
		for i := 0; i < l.Length-pos-n; i++ {
			l.SetCell(pos+i, l.LoadCell(pos+n+i, cell))
		}
		for i := l.Length - n; i < l.Length; i++ {
			l.SetCell(i, fillCellData)
		}
	} else {
		for i := pos; i < l.Length; i++ {
			l.SetCell(i, fillCellData)
		}
	}

	if pos > 0 && l.GetWidth(pos-1) == 2 {
		l.SetCellFromCodepoint(pos-1, 0, 1, &fillCellData.AttributeData)
	}
	if l.GetWidth(pos) == 0 && !l.HasContent(pos) {
		l.SetCellFromCodepoint(pos, 0, 1, &fillCellData.AttributeData)
	}
}

// ReplaceCells fills cells from start (inclusive) to end (exclusive).
func (l *BufferLine) ReplaceCells(start, end int, fillCellData *CellData, respectProtect bool) {
	if respectProtect {
		if start > 0 && l.GetWidth(start-1) == 2 && !l.IsProtected(start-1) {
			l.SetCellFromCodepoint(start-1, 0, 1, &fillCellData.AttributeData)
		}
		if end < l.Length && l.GetWidth(end-1) == 2 && !l.IsProtected(end) {
			l.SetCellFromCodepoint(end, 0, 1, &fillCellData.AttributeData)
		}
		for start < end && start < l.Length {
			if !l.IsProtected(start) {
				l.SetCell(start, fillCellData)
			}
			start++
		}
		return
	}

	if start > 0 && l.GetWidth(start-1) == 2 {
		l.SetCellFromCodepoint(start-1, 0, 1, &fillCellData.AttributeData)
	}
	if end < l.Length && l.GetWidth(end-1) == 2 {
		l.SetCellFromCodepoint(end, 0, 1, &fillCellData.AttributeData)
	}

	for start < end && start < l.Length {
		l.SetCell(start, fillCellData)
		start++
	}
}

// Resize the line to cols, filling new cells with fillCellData.
func (l *BufferLine) Resize(cols int, fillCellData *CellData) {
	if cols == l.Length {
		return
	}
	uint32Cells := cols * cellSize
	if cols > l.Length {
		if cap(l.data) >= uint32Cells {
			l.data = l.data[:uint32Cells]
		} else {
			data := make([]uint32, uint32Cells)
			copy(data, l.data)
			l.data = data
		}
		for i := l.Length; i < cols; i++ {
			l.SetCell(i, fillCellData)
		}
	} else {
		l.data = l.data[:uint32Cells]
		for key := range l.combined {
			if key >= cols {
				delete(l.combined, key)
			}
		}
		for key := range l.extendedAttrs {
			if key >= cols {
				delete(l.extendedAttrs, key)
			}
		}
	}
	l.Length = cols
}

// Fill fills the whole line with fillCellData.
func (l *BufferLine) Fill(fillCellData *CellData, respectProtect bool) {
	if respectProtect {
		for i := 0; i < l.Length; i++ {
			if !l.IsProtected(i) {
				l.SetCell(i, fillCellData)
			}
		}
		return
	}
	l.combined = map[int]string{}
	l.extendedAttrs = map[int]*ExtendedAttrs{}
	for i := 0; i < l.Length; i++ {
		l.SetCell(i, fillCellData)
	}
}

// CopyFrom makes this line a full copy of line.
func (l *BufferLine) CopyFrom(line *BufferLine) {
	if l.Length != line.Length {
		l.data = make([]uint32, len(line.data))
	}
	copy(l.data, line.data)
	l.Length = line.Length
	l.combined = map[int]string{}
	for k, v := range line.combined {
		l.combined[k] = v
	}
	l.extendedAttrs = map[int]*ExtendedAttrs{}
	for k, v := range line.extendedAttrs {
		l.extendedAttrs[k] = v
	}
	l.IsWrapped = line.IsWrapped
}

// Clone returns a copy of the line.
func (l *BufferLine) Clone() *BufferLine {
	newLine := &BufferLine{
		data:          make([]uint32, len(l.data)),
		combined:      map[int]string{},
		extendedAttrs: map[int]*ExtendedAttrs{},
		Length:        l.Length,
		IsWrapped:     l.IsWrapped,
	}
	copy(newLine.data, l.data)
	for k, v := range l.combined {
		newLine.combined[k] = v
	}
	for k, v := range l.extendedAttrs {
		newLine.extendedAttrs[k] = v
	}
	return newLine
}

// GetTrimmedLength returns the line length without trailing empty cells.
func (l *BufferLine) GetTrimmedLength() int {
	for i := l.Length - 1; i >= 0; i-- {
		if l.data[i*cellSize+cellContent]&ContentHasContentMask != 0 {
			return i + int(l.data[i*cellSize+cellContent]>>ContentWidthShift)
		}
	}
	return 0
}

// GetNoBgTrimmedLength is like GetTrimmedLength but keeps cells with a
// non-default background.
func (l *BufferLine) GetNoBgTrimmedLength() int {
	for i := l.Length - 1; i >= 0; i-- {
		if l.data[i*cellSize+cellContent]&ContentHasContentMask != 0 ||
			l.data[i*cellSize+cellBG]&AttrCMMask != 0 {
			return i + int(l.data[i*cellSize+cellContent]>>ContentWidthShift)
		}
	}
	return 0
}

// CopyCellsFrom copies length cells from src starting at srcCol into this
// line at destCol.
func (l *BufferLine) CopyCellsFrom(src *BufferLine, srcCol, destCol, length int, applyInReverse bool) {
	srcData := src.data
	if applyInReverse {
		for cell := length - 1; cell >= 0; cell-- {
			for i := 0; i < cellSize; i++ {
				l.data[(destCol+cell)*cellSize+i] = srcData[(srcCol+cell)*cellSize+i]
			}
			if srcData[(srcCol+cell)*cellSize+cellBG]&BgHasExtended != 0 {
				l.extendedAttrs[destCol+cell] = src.extendedAttrs[srcCol+cell]
			}
		}
	} else {
		for cell := 0; cell < length; cell++ {
			for i := 0; i < cellSize; i++ {
				l.data[(destCol+cell)*cellSize+i] = srcData[(srcCol+cell)*cellSize+i]
			}
			if srcData[(srcCol+cell)*cellSize+cellBG]&BgHasExtended != 0 {
				l.extendedAttrs[destCol+cell] = src.extendedAttrs[srcCol+cell]
			}
		}
	}

	// move any combined data over as needed
	for key, value := range src.combined {
		if key >= srcCol {
			l.combined[key-srcCol+destCol] = value
		}
	}
}

// TranslateToString renders the line as a string.
func (l *BufferLine) TranslateToString(trimRight bool, startCol, endCol int) string {
	if endCol < 0 || endCol > l.Length {
		endCol = l.Length
	}
	if trimRight {
		trimmed := l.GetTrimmedLength()
		if trimmed < endCol {
			endCol = trimmed
		}
	}
	result := ""
	for startCol < endCol {
		content := l.data[startCol*cellSize+cellContent]
		cp := content & ContentCodepointMask
		switch {
		case content&ContentIsCombinedMask != 0:
			result += l.combined[startCol]
		case cp != 0:
			result += string(rune(cp))
		default:
			result += string(rune(WhitespaceCellChar))
		}
		advance := int(content >> ContentWidthShift)
		if advance == 0 {
			advance = 1 // always advance by at least 1
		}
		startCol += advance
	}
	return result
}

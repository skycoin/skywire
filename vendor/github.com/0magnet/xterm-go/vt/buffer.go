package vt

// maxBufferSize caps the scrollback length (xterm.js uses 2^32-1; we
// clamp to int32 max so the constant also fits tinygo's 32-bit int).
const maxBufferSize = 1<<31 - 1

// Charset maps input glyphs to replacement strings (port of ICharset).
type Charset map[byte]rune

// Marker tracks a line position in the buffer across scrolling
// (simplified port of Marker).
type Marker struct {
	Line     int
	disposed bool
}

// IsDisposed reports whether the marker was disposed.
func (m *Marker) IsDisposed() bool { return m.disposed }

// Dispose invalidates the marker.
func (m *Marker) Dispose() { m.disposed = true }

// Buffer represents a terminal buffer: text content, cursor position and
// scroll state (port of Buffer).
type Buffer struct {
	Lines            *CircularList[*BufferLine]
	YDisp            int
	YBase            int
	Y                int
	X                int
	ScrollBottom     int
	ScrollTop        int
	Tabs             map[int]bool
	SavedY           int
	SavedX           int
	SavedCurAttrData *AttributeData
	SavedCharset     Charset
	Markers          []*Marker

	nullCell       *CellData
	whitespaceCell *CellData
	hasScrollback  bool
	opts           *Options
	cols           int
	rows           int
}

// NewBuffer creates a buffer.
func NewBuffer(hasScrollback bool, opts *Options, cols, rows int) *Buffer {
	b := &Buffer{
		Tabs:             map[int]bool{},
		SavedCurAttrData: NewAttributeData(),
		nullCell:         CellDataFromCharData(CharData{0, "", NullCellWidth, NullCellCode}),
		whitespaceCell:   CellDataFromCharData(CharData{0, " ", WhitespaceCellWidth, WhitespaceCellCode}),
		hasScrollback:    hasScrollback,
		opts:             opts,
		cols:             cols,
		rows:             rows,
	}
	b.Lines = NewCircularList[*BufferLine](b.correctBufferLength(rows))
	b.Lines.OnTrim = b.onLinesTrim
	b.Lines.OnInsert = b.onLinesInsert
	b.Lines.OnDelete = b.onLinesDelete
	b.ScrollTop = 0
	b.ScrollBottom = rows - 1
	b.SetupTabStops(-1)
	return b
}

// marker adjustments replacing the JS per-marker event registrations
func (b *Buffer) onLinesTrim(amount int) {
	for _, marker := range b.Markers {
		marker.Line -= amount
		if marker.Line < 0 {
			marker.Dispose()
		}
	}
	b.pruneMarkers()
}

func (b *Buffer) onLinesInsert(index, amount int) {
	for _, marker := range b.Markers {
		if marker.Line >= index {
			marker.Line += amount
		}
	}
}

func (b *Buffer) onLinesDelete(index, amount int) {
	for _, marker := range b.Markers {
		if marker.Line >= index && marker.Line < index+amount {
			marker.Dispose()
		}
		if marker.Line > index {
			marker.Line -= amount
		}
	}
	b.pruneMarkers()
}

func (b *Buffer) pruneMarkers() {
	kept := b.Markers[:0]
	for _, marker := range b.Markers {
		if !marker.IsDisposed() {
			kept = append(kept, marker)
		}
	}
	b.Markers = kept
}

// GetNullCell returns the shared null cell carrying attr.
func (b *Buffer) GetNullCell(attr *AttributeData) *CellData {
	if attr != nil {
		b.nullCell.Fg = attr.Fg
		b.nullCell.Bg = attr.Bg
		b.nullCell.Extended = attr.Extended
	} else {
		b.nullCell.Fg = 0
		b.nullCell.Bg = 0
		b.nullCell.Extended = NewExtendedAttrs()
	}
	return b.nullCell
}

// GetWhitespaceCell returns the shared whitespace cell carrying attr.
func (b *Buffer) GetWhitespaceCell(attr *AttributeData) *CellData {
	if attr != nil {
		b.whitespaceCell.Fg = attr.Fg
		b.whitespaceCell.Bg = attr.Bg
		b.whitespaceCell.Extended = attr.Extended
	} else {
		b.whitespaceCell.Fg = 0
		b.whitespaceCell.Bg = 0
		b.whitespaceCell.Extended = NewExtendedAttrs()
	}
	return b.whitespaceCell
}

// GetBlankLine returns a fresh blank line filled with attr.
func (b *Buffer) GetBlankLine(attr *AttributeData, isWrapped bool) *BufferLine {
	return NewBufferLine(b.cols, b.GetNullCell(attr), isWrapped)
}

// HasScrollback reports whether the buffer has scrollback capacity.
func (b *Buffer) HasScrollback() bool {
	return b.hasScrollback && b.Lines.MaxLength() > b.rows
}

// IsCursorInViewport reports whether the cursor is visible.
func (b *Buffer) IsCursorInViewport() bool {
	absoluteY := b.YBase + b.Y
	relativeY := absoluteY - b.YDisp
	return relativeY >= 0 && relativeY < b.rows
}

func (b *Buffer) correctBufferLength(rows int) int {
	if !b.hasScrollback {
		return rows
	}
	correct := rows + b.opts.Scrollback
	if correct > maxBufferSize {
		return maxBufferSize
	}
	return correct
}

// FillViewportRows fills the viewport with blank lines.
func (b *Buffer) FillViewportRows(fillAttr *AttributeData) {
	if b.Lines.Length() == 0 {
		if fillAttr == nil {
			fillAttr = NewAttributeData()
		}
		for i := 0; i < b.rows; i++ {
			b.Lines.Push(b.GetBlankLine(fillAttr, false))
		}
	}
}

// Clear resets the buffer to its initial state.
func (b *Buffer) Clear() {
	b.YDisp = 0
	b.YBase = 0
	b.Y = 0
	b.X = 0
	b.Lines = NewCircularList[*BufferLine](b.correctBufferLength(b.rows))
	b.Lines.OnTrim = b.onLinesTrim
	b.Lines.OnInsert = b.onLinesInsert
	b.Lines.OnDelete = b.onLinesDelete
	b.ScrollTop = 0
	b.ScrollBottom = b.rows - 1
	b.SetupTabStops(-1)
}

// Resize the buffer, adjusting its data accordingly.
func (b *Buffer) Resize(newCols, newRows int) {
	nullCell := b.GetNullCell(NewAttributeData())

	newMaxLength := b.correctBufferLength(newRows)
	if newMaxLength > b.Lines.MaxLength() {
		b.Lines.SetMaxLength(newMaxLength)
	}

	if b.Lines.Length() > 0 {
		// deal with columns increasing (reducing needs to happen after reflow)
		if b.cols < newCols {
			for i := 0; i < b.Lines.Length(); i++ {
				b.Lines.Get(i).Resize(newCols, nullCell)
			}
		}

		// resize rows in both directions as needed
		addToY := 0
		if b.rows < newRows {
			for y := b.rows; y < newRows; y++ {
				if b.Lines.Length() < newRows+b.YBase {
					if b.YBase > 0 && b.Lines.Length() <= b.YBase+b.Y+addToY+1 {
						// there is room above the buffer and no empty lines
						// below the cursor: scroll up
						b.YBase--
						addToY++
						if b.YDisp > 0 {
							b.YDisp--
						}
					} else {
						b.Lines.Push(NewBufferLine(newCols, nullCell, false))
					}
				}
			}
		} else {
			for y := b.rows; y > newRows; y-- {
				if b.Lines.Length() > newRows+b.YBase {
					if b.Lines.Length() > b.YBase+b.Y+1 {
						// blank line below the cursor, remove it
						b.Lines.Pop()
					} else {
						// the line is the cursor, scroll down
						b.YBase++
						b.YDisp++
					}
				}
			}
		}

		// reduce max length if needed after adjustments
		if newMaxLength < b.Lines.MaxLength() {
			amountToTrim := b.Lines.Length() - newMaxLength
			if amountToTrim > 0 {
				b.Lines.TrimStart(amountToTrim)
				b.YBase = maxInt(b.YBase-amountToTrim, 0)
				b.YDisp = maxInt(b.YDisp-amountToTrim, 0)
				b.SavedY = maxInt(b.SavedY-amountToTrim, 0)
			}
			b.Lines.SetMaxLength(newMaxLength)
		}

		// make sure the cursor stays on screen
		b.X = min(b.X, newCols-1)
		b.Y = min(b.Y, newRows-1)
		if addToY != 0 {
			b.Y += addToY
		}
		b.SavedX = min(b.SavedX, newCols-1)

		b.ScrollTop = 0
	}

	b.ScrollBottom = newRows - 1

	if b.isReflowEnabled() {
		b.reflow(newCols, newRows)

		// trim the end of the line off if cols shrunk
		if b.cols > newCols {
			for i := 0; i < b.Lines.Length(); i++ {
				b.Lines.Get(i).Resize(newCols, nullCell)
			}
		}
	}

	b.cols = newCols
	b.rows = newRows
}

func (b *Buffer) isReflowEnabled() bool {
	return b.hasScrollback && !b.opts.WindowsMode
}

func (b *Buffer) reflow(newCols, newRows int) {
	if b.cols == newCols {
		return
	}
	if newCols > b.cols {
		b.reflowLarger(newCols, newRows)
	} else {
		b.reflowSmaller(newCols, newRows)
	}
}

func (b *Buffer) reflowLarger(newCols, newRows int) {
	toRemove := reflowLargerGetLinesToRemove(b.Lines, b.cols, newCols, b.YBase+b.Y, b.GetNullCell(NewAttributeData()), b.opts.ReflowCursorLine)
	if len(toRemove) > 0 {
		newLayoutResult := reflowLargerCreateNewLayout(b.Lines, toRemove)
		reflowLargerApplyNewLayout(b.Lines, newLayoutResult.layout)
		b.reflowLargerAdjustViewport(newCols, newRows, newLayoutResult.countRemoved)
	}
}

func (b *Buffer) reflowLargerAdjustViewport(newCols, newRows, countRemoved int) {
	nullCell := b.GetNullCell(NewAttributeData())
	viewportAdjustments := countRemoved
	for viewportAdjustments > 0 {
		viewportAdjustments--
		if b.YBase == 0 {
			if b.Y > 0 {
				b.Y--
			}
			if b.Lines.Length() < newRows {
				b.Lines.Push(NewBufferLine(newCols, nullCell, false))
			}
		} else {
			if b.YDisp == b.YBase {
				b.YDisp--
			}
			b.YBase--
		}
	}
	b.SavedY = maxInt(b.SavedY-countRemoved, 0)
}

type bufferInsertion struct {
	start    int
	newLines []*BufferLine
}

func (b *Buffer) reflowSmaller(newCols, newRows int) {
	nullCell := b.GetNullCell(NewAttributeData())
	var toInsert []bufferInsertion
	countToInsert := 0
	// go backwards as many lines may be trimmed
	for y := b.Lines.Length() - 1; y >= 0; y-- {
		nextLine := b.Lines.Get(y)
		if nextLine == nil || (!nextLine.IsWrapped && nextLine.GetTrimmedLength() <= newCols) {
			continue
		}

		// gather wrapped lines and adjust y to be the starting line
		wrappedLines := []*BufferLine{nextLine}
		for nextLine.IsWrapped && y > 0 {
			y--
			nextLine = b.Lines.Get(y)
			wrappedLines = append([]*BufferLine{nextLine}, wrappedLines...)
		}

		if !b.opts.ReflowCursorLine {
			// if these lines contain the cursor don't touch them
			absoluteY := b.YBase + b.Y
			if absoluteY >= y && absoluteY < y+len(wrappedLines) {
				continue
			}
		}

		lastLineLength := wrappedLines[len(wrappedLines)-1].GetTrimmedLength()
		destLineLengths := reflowSmallerGetNewLineLengths(wrappedLines, b.cols, newCols)
		linesToAdd := len(destLineLengths) - len(wrappedLines)
		var trimmedLines int
		if b.YBase == 0 && b.Y != b.Lines.Length()-1 {
			trimmedLines = maxInt(0, b.Y-b.Lines.MaxLength()+linesToAdd)
		} else {
			trimmedLines = maxInt(0, b.Lines.Length()-b.Lines.MaxLength()+linesToAdd)
		}

		// add the new lines
		var newLines []*BufferLine
		for i := 0; i < linesToAdd; i++ {
			newLine := b.GetBlankLine(NewAttributeData(), true)
			newLines = append(newLines, newLine)
		}
		if len(newLines) > 0 {
			toInsert = append(toInsert, bufferInsertion{
				start:    y + len(wrappedLines) + countToInsert,
				newLines: newLines,
			})
			countToInsert += len(newLines)
		}
		wrappedLines = append(wrappedLines, newLines...)

		// copy buffer data to new locations (backwards, in-place)
		destLineIndex := len(destLineLengths) - 1
		destCol := destLineLengths[destLineIndex]
		if destCol == 0 {
			destLineIndex--
			destCol = destLineLengths[destLineIndex]
		}
		srcLineIndex := len(wrappedLines) - linesToAdd - 1
		srcCol := lastLineLength
		for srcLineIndex >= 0 {
			cellsToCopy := min(srcCol, destCol)
			if destLineIndex < 0 || destLineIndex >= len(wrappedLines) {
				break
			}
			wrappedLines[destLineIndex].CopyCellsFrom(wrappedLines[srcLineIndex], srcCol-cellsToCopy, destCol-cellsToCopy, cellsToCopy, true)
			destCol -= cellsToCopy
			if destCol == 0 {
				destLineIndex--
				if destLineIndex >= 0 {
					destCol = destLineLengths[destLineIndex]
				}
			}
			srcCol -= cellsToCopy
			if srcCol == 0 {
				srcLineIndex--
				wrappedLinesIndex := maxInt(srcLineIndex, 0)
				srcCol = getWrappedLineTrimmedLength(wrappedLines, wrappedLinesIndex, b.cols)
			}
		}

		// null out the end of the line ends if a wide character wrapped to
		// the following line
		for i := 0; i < len(wrappedLines); i++ {
			if i < len(destLineLengths) && destLineLengths[i] < newCols {
				wrappedLines[i].SetCell(destLineLengths[i], nullCell)
			}
		}

		// adjust viewport as needed
		viewportAdjustments := linesToAdd - trimmedLines
		for viewportAdjustments > 0 {
			viewportAdjustments--
			if b.YBase == 0 {
				if b.Y < newRows-1 {
					b.Y++
					b.Lines.Pop()
				} else {
					b.YBase++
					b.YDisp++
				}
			} else {
				if b.YBase < min(b.Lines.MaxLength(), b.Lines.Length()+countToInsert)-newRows {
					if b.YBase == b.YDisp {
						b.YDisp++
					}
					b.YBase++
				}
			}
		}
		b.SavedY = min(b.SavedY+linesToAdd, b.YBase+newRows-1)
	}

	// rearrange lines in the buffer for insertions in a single pass
	if len(toInsert) > 0 {
		type insertEvent struct{ index, amount int }
		var insertEvents []insertEvent

		originalLines := make([]*BufferLine, b.Lines.Length())
		for i := 0; i < b.Lines.Length(); i++ {
			originalLines[i] = b.Lines.Get(i)
		}
		originalLinesLength := b.Lines.Length()

		originalLineIndex := originalLinesLength - 1
		nextToInsertIndex := 0
		var nextToInsert *bufferInsertion
		if nextToInsertIndex < len(toInsert) {
			nextToInsert = &toInsert[nextToInsertIndex]
		}
		b.Lines.SetLength(min(b.Lines.MaxLength(), b.Lines.Length()+countToInsert))
		countInsertedSoFar := 0
		for i := min(b.Lines.MaxLength()-1, originalLinesLength+countToInsert-1); i >= 0; i-- {
			if nextToInsert != nil && nextToInsert.start > originalLineIndex+countInsertedSoFar {
				// insert extra lines here, adjusting i as needed
				//
				// i is bounded here as well as by the outer loop. Narrowing to
				// very few columns wraps one original line into more lines than
				// the buffer can hold, and without the bound this walks i below
				// zero and panics with an index of -1. Lines that no longer fit
				// are dropped, which is what the trim below already assumes.
				for nextI := len(nextToInsert.newLines) - 1; nextI >= 0 && i >= 0; nextI-- {
					b.Lines.Set(i, nextToInsert.newLines[nextI])
					i--
				}
				i++

				insertEvents = append(insertEvents, insertEvent{
					index:  originalLineIndex + 1,
					amount: len(nextToInsert.newLines),
				})

				countInsertedSoFar += len(nextToInsert.newLines)
				nextToInsertIndex++
				if nextToInsertIndex < len(toInsert) {
					nextToInsert = &toInsert[nextToInsertIndex]
				} else {
					nextToInsert = nil
				}
			} else {
				b.Lines.Set(i, originalLines[originalLineIndex])
				originalLineIndex--
			}
		}

		// update markers
		insertCountEmitted := 0
		for i := len(insertEvents) - 1; i >= 0; i-- {
			insertEvents[i].index += insertCountEmitted
			b.onLinesInsert(insertEvents[i].index, insertEvents[i].amount)
			insertCountEmitted += insertEvents[i].amount
		}
		amountToTrim := maxInt(0, originalLinesLength+countToInsert-b.Lines.MaxLength())
		if amountToTrim > 0 {
			b.onLinesTrim(amountToTrim)
		}
	}
}

// TranslateBufferLineToString renders a buffer line as a string.
func (b *Buffer) TranslateBufferLineToString(lineIndex int, trimRight bool, startCol, endCol int) string {
	line := b.Lines.Get(lineIndex)
	if line == nil {
		return ""
	}
	return line.TranslateToString(trimRight, startCol, endCol)
}

// GetWrappedRangeForLine returns the first and last row of the wrapped
// line group containing y.
func (b *Buffer) GetWrappedRangeForLine(y int) (first, last int) {
	first = y
	last = y
	for first > 0 && b.Lines.Get(first).IsWrapped {
		first--
	}
	for last+1 < b.Lines.Length() && b.Lines.Get(last+1).IsWrapped {
		last++
	}
	return first, last
}

// SetupTabStops sets up the tab stops (i < 0 = from scratch).
func (b *Buffer) SetupTabStops(i int) {
	if i >= 0 {
		if !b.Tabs[i] {
			i = b.PrevStop(i)
		}
	} else {
		b.Tabs = map[int]bool{}
		i = 0
	}
	for ; i < b.cols; i += b.opts.TabStopWidth {
		b.Tabs[i] = true
	}
}

// PrevStop returns the previous tab stop from x (or the cursor with -1).
func (b *Buffer) PrevStop(x int) int {
	if x < 0 {
		x = b.X
	}
	x--
	for !b.Tabs[x] && x > 0 {
		x--
	}
	if x >= b.cols {
		return b.cols - 1
	}
	if x < 0 {
		return 0
	}
	return x
}

// NextStop returns the next tab stop from x (or the cursor with -1).
func (b *Buffer) NextStop(x int) int {
	if x < 0 {
		x = b.X
	}
	x++
	for !b.Tabs[x] && x < b.cols {
		x++
	}
	if x >= b.cols {
		return b.cols - 1
	}
	if x < 0 {
		return 0
	}
	return x
}

// AddMarker adds a line marker.
func (b *Buffer) AddMarker(y int) *Marker {
	marker := &Marker{Line: y}
	b.Markers = append(b.Markers, marker)
	return marker
}

// ClearMarkers clears markers on a single line.
func (b *Buffer) ClearMarkers(y int) {
	for _, marker := range b.Markers {
		if marker.Line == y {
			marker.Dispose()
		}
	}
	b.pruneMarkers()
}

// ClearAllMarkers clears all markers.
func (b *Buffer) ClearAllMarkers() {
	for _, marker := range b.Markers {
		marker.Dispose()
	}
	b.Markers = nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

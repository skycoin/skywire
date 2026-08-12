package vt

// newLayoutResult is the result of reflowLargerCreateNewLayout.
type newLayoutResult struct {
	layout       []int
	countRemoved int
}

// reflowLargerGetLinesToRemove evaluates and returns indexes to be removed
// after a reflow-larger occurs (wrapped lines unwrapping). The result is
// (index, count) pairs.
func reflowLargerGetLinesToRemove(lines *CircularList[*BufferLine], oldCols, newCols, bufferAbsoluteY int, nullCell *CellData, reflowCursorLine bool) []int {
	var toRemove []int

	for y := 0; y < lines.Length()-1; y++ {
		// check if this row is wrapped
		i := y
		i++
		nextLine := lines.Get(i)
		if !nextLine.IsWrapped {
			continue
		}

		// check how many lines it's wrapped for
		wrappedLines := []*BufferLine{lines.Get(y)}
		for i < lines.Length() && nextLine.IsWrapped {
			wrappedLines = append(wrappedLines, nextLine)
			i++
			if i < lines.Length() {
				nextLine = lines.Get(i)
			}
		}

		if !reflowCursorLine {
			// if these lines contain the cursor don't touch them
			if bufferAbsoluteY >= y && bufferAbsoluteY < i {
				y += len(wrappedLines) - 1
				continue
			}
		}

		// copy buffer data to new locations
		destLineIndex := 0
		destCol := getWrappedLineTrimmedLength(wrappedLines, destLineIndex, oldCols)
		srcLineIndex := 1
		srcCol := 0
		for srcLineIndex < len(wrappedLines) {
			srcTrimmedLineLength := getWrappedLineTrimmedLength(wrappedLines, srcLineIndex, oldCols)
			srcRemainingCells := srcTrimmedLineLength - srcCol
			destRemainingCells := newCols - destCol
			cellsToCopy := min(srcRemainingCells, destRemainingCells)

			wrappedLines[destLineIndex].CopyCellsFrom(wrappedLines[srcLineIndex], srcCol, destCol, cellsToCopy, false)

			destCol += cellsToCopy
			if destCol == newCols {
				destLineIndex++
				destCol = 0
			}
			srcCol += cellsToCopy
			if srcCol == srcTrimmedLineLength {
				srcLineIndex++
				srcCol = 0
			}

			// make sure the last cell isn't wide, if it is copy it to the current dest
			if destCol == 0 && destLineIndex != 0 {
				if wrappedLines[destLineIndex-1].GetWidth(newCols-1) == 2 {
					wrappedLines[destLineIndex].CopyCellsFrom(wrappedLines[destLineIndex-1], newCols-1, destCol, 1, false)
					destCol++
					// null out the end of the last row
					wrappedLines[destLineIndex-1].SetCell(newCols-1, nullCell)
				}
			}
		}

		// clear out remaining cells or fragments could remain
		wrappedLines[destLineIndex].ReplaceCells(destCol, newCols, nullCell, false)

		// work backwards and remove any rows at the end that only contain null cells
		countToRemove := 0
		for i := len(wrappedLines) - 1; i > 0; i-- {
			if i > destLineIndex || wrappedLines[i].GetTrimmedLength() == 0 {
				countToRemove++
			} else {
				break
			}
		}

		if countToRemove > 0 {
			toRemove = append(toRemove, y+len(wrappedLines)-countToRemove) // index
			toRemove = append(toRemove, countToRemove)
		}

		y += len(wrappedLines) - 1
	}
	return toRemove
}

// reflowLargerCreateNewLayout creates the new layout for lines given the
// (index, count) pairs to remove.
func reflowLargerCreateNewLayout(lines *CircularList[*BufferLine], toRemove []int) newLayoutResult {
	var layout []int
	nextToRemoveIndex := 0
	nextToRemoveStart := -1
	if len(toRemove) > 0 {
		nextToRemoveStart = toRemove[0]
	}
	countRemovedSoFar := 0
	for i := 0; i < lines.Length(); i++ {
		if nextToRemoveStart == i {
			nextToRemoveIndex++
			countToRemove := toRemove[nextToRemoveIndex]

			// tell markers that there was a deletion
			if lines.OnDelete != nil {
				lines.OnDelete(i-countRemovedSoFar, countToRemove)
			}

			i += countToRemove - 1
			countRemovedSoFar += countToRemove
			nextToRemoveIndex++
			if nextToRemoveIndex < len(toRemove) {
				nextToRemoveStart = toRemove[nextToRemoveIndex]
			} else {
				nextToRemoveStart = -1
			}
		} else {
			layout = append(layout, i)
		}
	}
	return newLayoutResult{layout: layout, countRemoved: countRemovedSoFar}
}

// reflowLargerApplyNewLayout applies the new layout to the buffer in a
// single pass.
func reflowLargerApplyNewLayout(lines *CircularList[*BufferLine], newLayout []int) {
	newLayoutLines := make([]*BufferLine, len(newLayout))
	for i := range newLayout {
		newLayoutLines[i] = lines.Get(newLayout[i])
	}
	for i := range newLayoutLines {
		lines.Set(i, newLayoutLines[i])
	}
	lines.SetLength(len(newLayout))
}

// reflowSmallerGetNewLineLengths precomputes the wrapping points of a
// wrapped line group for the new column count (wide chars may need to
// wrap onto the following line).
func reflowSmallerGetNewLineLengths(wrappedLines []*BufferLine, oldCols, newCols int) []int {
	var newLineLengths []int
	cellsNeeded := 0
	for i := range wrappedLines {
		cellsNeeded += getWrappedLineTrimmedLength(wrappedLines, i, oldCols)
	}

	srcCol := 0
	srcLine := 0
	cellsAvailable := 0
	for cellsAvailable < cellsNeeded {
		if cellsNeeded-cellsAvailable < newCols {
			// add the final line and exit the loop
			newLineLengths = append(newLineLengths, cellsNeeded-cellsAvailable)
			break
		}
		srcCol += newCols
		oldTrimmedLength := getWrappedLineTrimmedLength(wrappedLines, srcLine, oldCols)
		if srcCol > oldTrimmedLength {
			srcCol -= oldTrimmedLength
			srcLine++
		}
		endsWithWide := wrappedLines[srcLine].GetWidth(srcCol-1) == 2
		if endsWithWide {
			srcCol--
		}
		lineLength := newCols
		if endsWithWide {
			lineLength = newCols - 1
		}
		newLineLengths = append(newLineLengths, lineLength)
		cellsAvailable += lineLength
	}

	return newLineLengths
}

// getWrappedLineTrimmedLength returns the used length of a row inside a
// wrapped line group.
func getWrappedLineTrimmedLength(lines []*BufferLine, i, cols int) int {
	// if this is the last row in the wrapped line, get the actual trimmed length
	if i == len(lines)-1 {
		return lines[i].GetTrimmedLength()
	}
	// detect whether the following line starts with a wide character and
	// the end of the current line is null
	endsInNull := !lines[i].HasContent(cols-1) && lines[i].GetWidth(cols-1) == 1
	followingLineStartsWithWide := lines[i+1].GetWidth(0) == 2
	if endsInNull && followingLineStartsWithWide {
		return cols - 1
	}
	return cols
}

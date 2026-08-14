package vt

import (
	"fmt"
	"strconv"
	"strings"
)

// Port of src/common/InputHandler.ts — the terminal's implementation
// of all VT semantics, wiring parser callbacks to buffer mutations.
// The async/Promise parse-stack machinery of the original is dropped
// (all handlers are synchronous in Go); event emitters become plain
// callback fields.
//
// Refer to http://invisible-island.net/xterm/ctlseqs/ctlseqs.html for
// the sequences handled here.

// C0/C1 control codes used by the handler.
const (
	c0ESC = "\x1b"
	c0BEL = 0x07
	c0BS  = 0x08
	c0HT  = 0x09
	c0LF  = 0x0a
	c0VT  = 0x0b
	c0FF  = 0x0c
	c0CR  = 0x0d
	c0SO  = 0x0e
	c0SI  = 0x0f
	c1IND = 0x84
	c1NEL = 0x85
	c1HTS = 0x88
)

// Max length of the UTF32 input buffer.
const maxParseBufferLength = 131072

// Limit length of title and icon name stacks.
const titleStackLimit = 10

// glevelMap maps the charset collect char to the G level.
var glevelMap = map[byte]int{'(': 0, ')': 1, '*': 2, '+': 3, '-': 1, '.': 2}

// Window options report types (WindowsOptionsReportType).
const (
	ReportWinSizePixels  = 0
	ReportCellSizePixels = 1
)

// Special color indexes for OSC 10/11/12 events.
const (
	SpecialColorForeground = 256
	SpecialColorBackground = 257
	SpecialColorCursor     = 258
)

// ColorRequestType describes what an OSC color event asks for.
type ColorRequestType int

// Color request types (ColorRequestType).
const (
	ColorRequestReport  ColorRequestType = 0
	ColorRequestSet     ColorRequestType = 1
	ColorRequestRestore ColorRequestType = 2
)

// ColorEvent is one entry of an OSC 4/10/11/12/104/110/111/112 color
// request. Index is -1 for "all colors" (OSC 104 without params).
type ColorEvent struct {
	Type  ColorRequestType
	Index int
	Color [3]int
}

// paramToWindowOption gates CSI t params on the enabled options.
func paramToWindowOption(n int, opts WindowOptions) bool {
	if n > 24 {
		return opts.SetWinLines
	}
	switch n {
	case 1:
		return opts.RestoreWin
	case 2:
		return opts.MinimizeWin
	case 3:
		return opts.SetWinPosition
	case 4:
		return opts.SetWinSizePixels
	case 5:
		return opts.RaiseWin
	case 6:
		return opts.LowerWin
	case 7:
		return opts.RefreshWin
	case 8:
		return opts.SetWinSizeChars
	case 9:
		return opts.MaximizeWin
	case 10:
		return opts.FullscreenWin
	case 11:
		return opts.GetWinState
	case 13:
		return opts.GetWinPosition
	case 14:
		return opts.GetWinSizePixels
	case 15:
		return opts.GetScreenSizePixels
	case 16:
		return opts.GetCellSizePixels
	case 18:
		return opts.GetWinSizeChars
	case 19:
		return opts.GetScreenSizeChars
	case 20:
		return opts.GetIconTitle
	case 21:
		return opts.GetWinTitle
	case 22:
		return opts.PushTitle
	case 23:
		return opts.PopTitle
	case 24:
		return opts.SetWinLines
	}
	return false
}

// paramAt returns params[i] or 0 when absent.
func paramAt(params *Params, i int) int {
	if i < params.Length {
		return int(params.Params[i])
	}
	return 0
}

// paramOr1 returns params[i] with 0/absent treated as 1 (the common
// `params.params[0] || 1` idiom).
func paramOr1(params *Params, i int) int {
	if v := paramAt(params, i); v != 0 {
		return v
	}
	return 1
}

// dirtyRowTracker tracks the viewport row range changed by a parse
// call.
type dirtyRowTracker struct {
	start, end    int
	bufferService *BufferService
}

func (d *dirtyRowTracker) clearRange() {
	d.start = d.bufferService.Buffer().Y
	d.end = d.bufferService.Buffer().Y
}

func (d *dirtyRowTracker) markDirty(y int) {
	if y < d.start {
		d.start = y
	} else if y > d.end {
		d.end = y
	}
}

func (d *dirtyRowTracker) markRangeDirty(y1, y2 int) {
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	if y1 < d.start {
		d.start = y1
	}
	if y2 > d.end {
		d.end = y2
	}
}

func (d *dirtyRowTracker) markAllDirty() {
	d.markRangeDirty(0, d.bufferService.Rows-1)
}

// InputHandler interprets all input from the parser.
type InputHandler struct {
	parser         *Parser
	bufferService  *BufferService
	charsetService *CharsetService
	coreService    *CoreService
	mouseService   *CoreMouseService
	oscLinkService *OscLinkService
	options        *Options

	activeBuffer *Buffer

	parseBuffer   []uint32
	stringDecoder StringToUtf32
	utf8Decoder   Utf8ToUtf32

	windowTitle      string
	iconName         string
	windowTitleStack []string
	iconNameStack    []string

	curAttrData           *AttributeData
	eraseAttrDataInternal *AttributeData

	dirtyTracker dirtyRowTracker

	// Event callbacks (Emitter fields of the original).
	OnRequestBell                 func()
	OnRequestRefreshRows          func(start, end int) // -1,-1 = full refresh
	OnRequestReset                func()
	OnRequestSendFocus            func()
	OnRequestSyncScrollBar        func()
	OnRequestWindowsOptionsReport func(reportType int)
	OnA11yChar                    func(char string)
	OnA11yTab                     func(spaces int)
	OnCursorMove                  func()
	OnLineFeed                    func()
	OnScroll                      func(ydisp int)
	OnTitleChange                 func(title string)
	OnColor                       func(events []ColorEvent)
}

// NewInputHandler creates the handler and registers all sequence
// handlers on the parser.
func NewInputHandler(bufferService *BufferService, charsetService *CharsetService, coreService *CoreService, mouseService *CoreMouseService, oscLinkService *OscLinkService, options *Options) *InputHandler {
	h := &InputHandler{
		parser:                NewParser(),
		bufferService:         bufferService,
		charsetService:        charsetService,
		coreService:           coreService,
		mouseService:          mouseService,
		oscLinkService:        oscLinkService,
		options:               options,
		parseBuffer:           make([]uint32, 4096),
		curAttrData:           NewAttributeData(),
		eraseAttrDataInternal: NewAttributeData(),
	}
	h.dirtyTracker = dirtyRowTracker{bufferService: bufferService}
	h.dirtyTracker.clearRange()

	// Track the active buffer manually (perf-critical in the original);
	// chain onto any callback the buffer service already installed.
	h.activeBuffer = bufferService.Buffer()
	prevActivate := bufferService.Buffers.OnBufferActivate
	bufferService.Buffers.OnBufferActivate = func(active, inactive *Buffer) {
		if prevActivate != nil {
			prevActivate(active, inactive)
		}
		h.activeBuffer = active
	}

	p := h.parser

	// print handler
	p.SetPrintHandler(h.Print)

	// CSI handlers
	p.RegisterCsiHandler(FunctionID{Final: "@"}, h.InsertChars)
	p.RegisterCsiHandler(FunctionID{Intermediates: " ", Final: "@"}, h.ScrollLeft)
	p.RegisterCsiHandler(FunctionID{Final: "A"}, h.CursorUp)
	p.RegisterCsiHandler(FunctionID{Intermediates: " ", Final: "A"}, h.ScrollRight)
	p.RegisterCsiHandler(FunctionID{Final: "B"}, h.CursorDown)
	p.RegisterCsiHandler(FunctionID{Final: "C"}, h.CursorForward)
	p.RegisterCsiHandler(FunctionID{Final: "D"}, h.CursorBackward)
	p.RegisterCsiHandler(FunctionID{Final: "E"}, h.CursorNextLine)
	p.RegisterCsiHandler(FunctionID{Final: "F"}, h.CursorPrecedingLine)
	p.RegisterCsiHandler(FunctionID{Final: "G"}, h.CursorCharAbsolute)
	p.RegisterCsiHandler(FunctionID{Final: "H"}, h.CursorPosition)
	p.RegisterCsiHandler(FunctionID{Final: "I"}, h.CursorForwardTab)
	p.RegisterCsiHandler(FunctionID{Final: "J"}, func(params *Params) bool { return h.EraseInDisplay(params, false) })
	p.RegisterCsiHandler(FunctionID{Prefix: "?", Final: "J"}, func(params *Params) bool { return h.EraseInDisplay(params, true) })
	p.RegisterCsiHandler(FunctionID{Final: "K"}, func(params *Params) bool { return h.EraseInLine(params, false) })
	p.RegisterCsiHandler(FunctionID{Prefix: "?", Final: "K"}, func(params *Params) bool { return h.EraseInLine(params, true) })
	p.RegisterCsiHandler(FunctionID{Final: "L"}, h.InsertLines)
	p.RegisterCsiHandler(FunctionID{Final: "M"}, h.DeleteLines)
	p.RegisterCsiHandler(FunctionID{Final: "P"}, h.DeleteChars)
	p.RegisterCsiHandler(FunctionID{Final: "S"}, h.ScrollUp)
	p.RegisterCsiHandler(FunctionID{Final: "T"}, h.ScrollDown)
	p.RegisterCsiHandler(FunctionID{Final: "X"}, h.EraseChars)
	p.RegisterCsiHandler(FunctionID{Final: "Z"}, h.CursorBackwardTab)
	p.RegisterCsiHandler(FunctionID{Final: "`"}, h.CharPosAbsolute)
	p.RegisterCsiHandler(FunctionID{Final: "a"}, h.HPositionRelative)
	p.RegisterCsiHandler(FunctionID{Final: "b"}, h.RepeatPrecedingCharacter)
	p.RegisterCsiHandler(FunctionID{Final: "c"}, h.SendDeviceAttributesPrimary)
	p.RegisterCsiHandler(FunctionID{Prefix: ">", Final: "c"}, h.SendDeviceAttributesSecondary)
	p.RegisterCsiHandler(FunctionID{Final: "d"}, h.LinePosAbsolute)
	p.RegisterCsiHandler(FunctionID{Final: "e"}, h.VPositionRelative)
	p.RegisterCsiHandler(FunctionID{Final: "f"}, h.HVPosition)
	p.RegisterCsiHandler(FunctionID{Final: "g"}, h.TabClear)
	p.RegisterCsiHandler(FunctionID{Final: "h"}, h.SetMode)
	p.RegisterCsiHandler(FunctionID{Prefix: "?", Final: "h"}, h.SetModePrivate)
	p.RegisterCsiHandler(FunctionID{Final: "l"}, h.ResetMode)
	p.RegisterCsiHandler(FunctionID{Prefix: "?", Final: "l"}, h.ResetModePrivate)
	p.RegisterCsiHandler(FunctionID{Final: "m"}, h.CharAttributes)
	p.RegisterCsiHandler(FunctionID{Final: "n"}, h.DeviceStatus)
	p.RegisterCsiHandler(FunctionID{Prefix: "?", Final: "n"}, h.DeviceStatusPrivate)
	p.RegisterCsiHandler(FunctionID{Intermediates: "!", Final: "p"}, h.SoftReset)
	p.RegisterCsiHandler(FunctionID{Intermediates: " ", Final: "q"}, h.SetCursorStyle)
	p.RegisterCsiHandler(FunctionID{Final: "r"}, h.SetScrollRegion)
	p.RegisterCsiHandler(FunctionID{Final: "s"}, func(params *Params) bool { return h.SaveCursor() })
	p.RegisterCsiHandler(FunctionID{Final: "t"}, h.WindowOptionsHandler)
	p.RegisterCsiHandler(FunctionID{Final: "u"}, func(params *Params) bool { return h.RestoreCursor() })
	p.RegisterCsiHandler(FunctionID{Intermediates: "'", Final: "}"}, h.InsertColumns)
	p.RegisterCsiHandler(FunctionID{Intermediates: "'", Final: "~"}, h.DeleteColumns)
	p.RegisterCsiHandler(FunctionID{Intermediates: "\"", Final: "q"}, h.SelectProtected)
	p.RegisterCsiHandler(FunctionID{Intermediates: "$", Final: "p"}, func(params *Params) bool { return h.RequestMode(params, true) })
	p.RegisterCsiHandler(FunctionID{Prefix: "?", Intermediates: "$", Final: "p"}, func(params *Params) bool { return h.RequestMode(params, false) })

	// execute handlers
	p.SetExecuteHandler(c0BEL, h.Bell)
	p.SetExecuteHandler(c0LF, h.LineFeed)
	p.SetExecuteHandler(c0VT, h.LineFeed)
	p.SetExecuteHandler(c0FF, h.LineFeed)
	p.SetExecuteHandler(c0CR, h.CarriageReturn)
	p.SetExecuteHandler(c0BS, h.Backspace)
	p.SetExecuteHandler(c0HT, h.Tab)
	p.SetExecuteHandler(c0SO, h.ShiftOut)
	p.SetExecuteHandler(c0SI, h.ShiftIn)

	p.SetExecuteHandler(c1IND, h.Index)
	p.SetExecuteHandler(c1NEL, h.NextLine)
	p.SetExecuteHandler(c1HTS, h.TabSet)

	// OSC handlers
	p.RegisterOscHandler(0, NewOscHandler(func(data string) bool { h.SetTitle(data); h.SetIconName(data); return true }))
	p.RegisterOscHandler(1, NewOscHandler(h.SetIconName))
	p.RegisterOscHandler(2, NewOscHandler(h.SetTitle))
	p.RegisterOscHandler(4, NewOscHandler(h.SetOrReportIndexedColor))
	p.RegisterOscHandler(8, NewOscHandler(h.SetHyperlink))
	p.RegisterOscHandler(10, NewOscHandler(h.SetOrReportFgColor))
	p.RegisterOscHandler(11, NewOscHandler(h.SetOrReportBgColor))
	p.RegisterOscHandler(12, NewOscHandler(h.SetOrReportCursorColor))
	p.RegisterOscHandler(104, NewOscHandler(h.RestoreIndexedColor))
	p.RegisterOscHandler(110, NewOscHandler(h.RestoreFgColor))
	p.RegisterOscHandler(111, NewOscHandler(h.RestoreBgColor))
	p.RegisterOscHandler(112, NewOscHandler(h.RestoreCursorColor))

	// ESC handlers
	p.RegisterEscHandler(FunctionID{Final: "7"}, h.SaveCursor)
	p.RegisterEscHandler(FunctionID{Final: "8"}, h.RestoreCursor)
	p.RegisterEscHandler(FunctionID{Final: "D"}, h.Index)
	p.RegisterEscHandler(FunctionID{Final: "E"}, h.NextLine)
	p.RegisterEscHandler(FunctionID{Final: "H"}, h.TabSet)
	p.RegisterEscHandler(FunctionID{Final: "M"}, h.ReverseIndex)
	p.RegisterEscHandler(FunctionID{Final: "="}, h.KeypadApplicationMode)
	p.RegisterEscHandler(FunctionID{Final: ">"}, h.KeypadNumericMode)
	p.RegisterEscHandler(FunctionID{Final: "c"}, h.FullReset)
	p.RegisterEscHandler(FunctionID{Final: "n"}, func() bool { return h.SetgLevel(2) })
	p.RegisterEscHandler(FunctionID{Final: "o"}, func() bool { return h.SetgLevel(3) })
	p.RegisterEscHandler(FunctionID{Final: "|"}, func() bool { return h.SetgLevel(3) })
	p.RegisterEscHandler(FunctionID{Final: "}"}, func() bool { return h.SetgLevel(2) })
	p.RegisterEscHandler(FunctionID{Final: "~"}, func() bool { return h.SetgLevel(1) })
	p.RegisterEscHandler(FunctionID{Intermediates: "%", Final: "@"}, h.SelectDefaultCharset)
	p.RegisterEscHandler(FunctionID{Intermediates: "%", Final: "G"}, h.SelectDefaultCharset)
	for flag := range Charsets {
		for _, collect := range []string{"(", ")", "*", "+", "-", ".", "/"} {
			c, f := collect, string(flag)
			p.RegisterEscHandler(FunctionID{Intermediates: c, Final: f}, func() bool { return h.SelectCharset(c + f) })
		}
	}
	p.RegisterEscHandler(FunctionID{Intermediates: "#", Final: "8"}, h.ScreenAlignmentPattern)

	// DCS handler
	p.RegisterDcsHandler(FunctionID{Intermediates: "$", Final: "q"}, NewDcsHandler(h.RequestStatusString))

	return h
}

// Parser exposes the parser for custom handler registration.
func (h *InputHandler) Parser() *Parser { return h.parser }

// GetAttrData returns the current attribute data.
func (h *InputHandler) GetAttrData() *AttributeData { return h.curAttrData }

func (h *InputHandler) getCurrentLinkID() int {
	return h.curAttrData.Extended.URLID
}

// Parse decodes and parses a chunk of UTF-8 pty output.
func (h *InputHandler) Parse(data []byte) {
	h.parseWith(len(data), func(start, end int) int {
		return h.utf8Decoder.Decode(data[start:end], h.parseBuffer)
	})
}

// ParseString parses a chunk given as a Go string (decoded via UTF-16
// like the JS string path to keep charCodeAt semantics for lone
// surrogates).
func (h *InputHandler) ParseString(data string) {
	units := utf16Units(data)
	h.parseWith(len(units), func(start, end int) int {
		return h.stringDecoder.Decode(units[start:end], h.parseBuffer)
	})
}

func (h *InputHandler) parseWith(length int, decode func(start, end int) int) {
	cursorStartX := h.activeBuffer.X
	cursorStartY := h.activeBuffer.Y

	// resize input buffer if needed
	if len(h.parseBuffer) < length && len(h.parseBuffer) < maxParseBufferLength {
		h.parseBuffer = make([]uint32, min(length, maxParseBufferLength))
	}

	// Clear the dirty row tracker so we know which lines changed
	h.dirtyTracker.clearRange()

	// process big data in smaller chunks
	for i := 0; i < length; i += maxParseBufferLength {
		end := min(i+maxParseBufferLength, length)
		n := decode(i, end)
		h.parser.Parse(h.parseBuffer, n)
	}

	if h.activeBuffer.X != cursorStartX || h.activeBuffer.Y != cursorStartY {
		if h.OnCursorMove != nil {
			h.OnCursorMove()
		}
	}

	// Refresh any dirty rows accumulated while parsing, clamped to the
	// viewport (relative to ydisp, not ybase).
	buffer := h.bufferService.Buffer()
	viewportEnd := h.dirtyTracker.end + buffer.YBase - buffer.YDisp
	viewportStart := h.dirtyTracker.start + buffer.YBase - buffer.YDisp
	if viewportStart < h.bufferService.Rows && h.OnRequestRefreshRows != nil {
		h.OnRequestRefreshRows(
			min(viewportStart, h.bufferService.Rows-1),
			min(viewportEnd, h.bufferService.Rows-1),
		)
	}
}

// Print writes decoded codepoints to the buffer (the parser's print
// handler).
func (h *InputHandler) Print(data []uint32, start, end int) {
	charset := h.charsetService.Charset
	cols := h.bufferService.Cols
	wraparoundMode := h.coreService.DecPrivateModes.Wraparound
	insertMode := h.coreService.Modes.InsertMode
	curAttr := h.curAttrData
	bufferRow := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + h.activeBuffer.Y)

	h.dirtyTracker.markDirty(h.activeBuffer.Y)

	// handle wide chars: reset start_cell-1 if we would overwrite the
	// second cell of a wide char
	if h.activeBuffer.X > 0 && end-start > 0 && bufferRow.GetWidth(h.activeBuffer.X-1) == 2 {
		bufferRow.SetCellFromCodepoint(h.activeBuffer.X-1, 0, 1, curAttr)
	}

	precedingJoinState := uint32(h.parser.PrecedingJoinState) // #nosec G115 -- Unicode codepoints and the parser's join state
	for pos := start; pos < end; pos++ {
		code := data[pos]

		// charset replacement is only defined for ASCII
		if code < 127 && charset != nil {
			if ch, ok := charset[byte(code)]; ok {
				code = uint32(ch) // #nosec G115 -- Unicode codepoints and the parser's join state
			}
		}

		currentInfo := CharProperties(code, precedingJoinState)
		chWidth := ExtractWidth(currentInfo)
		shouldJoin := ExtractShouldJoin(currentInfo)
		oldWidth := 0
		if shouldJoin {
			oldWidth = ExtractWidth(precedingJoinState)
		}
		precedingJoinState = currentInfo

		if h.options.ScreenReaderMode && h.OnA11yChar != nil {
			h.OnA11yChar(string(rune(code))) // #nosec G115 -- Unicode codepoints and the parser's join state
		}

		// goto next line if ch would overflow
		if h.activeBuffer.X+chWidth-oldWidth > cols {
			// autowrap - DECAWM
			if wraparoundMode {
				oldRow := bufferRow
				oldCol := h.activeBuffer.X - oldWidth
				h.activeBuffer.X = oldWidth
				h.activeBuffer.Y++
				if h.activeBuffer.Y == h.activeBuffer.ScrollBottom+1 {
					h.activeBuffer.Y--
					h.bufferService.Scroll(h.eraseAttrData(), true)
				} else {
					if h.activeBuffer.Y >= h.bufferService.Rows {
						h.activeBuffer.Y = h.bufferService.Rows - 1
					}
					// the line already exists, mark it as wrapped
					h.activeBuffer.Lines.Get(h.activeBuffer.YBase + h.activeBuffer.Y).IsWrapped = true
				}
				// row changed, get it again
				bufferRow = h.activeBuffer.Lines.Get(h.activeBuffer.YBase + h.activeBuffer.Y)
				if oldWidth > 0 {
					// combining character widens 1 column to 2: move
					// the old character to the next line
					bufferRow.CopyCellsFrom(oldRow, oldCol, 0, oldWidth, false)
				}
				// clear left over cells to the right
				for oldCol < cols {
					oldRow.SetCellFromCodepoint(oldCol, 0, 1, curAttr)
					oldCol++
				}
			} else {
				h.activeBuffer.X = cols - 1
				if chWidth == 2 {
					// wide char that does not fit into the last cell
					continue
				}
			}
		}

		// insert combining char at last cursor position
		if shouldJoin && h.activeBuffer.X > 0 {
			offset := 2
			if bufferRow.GetWidth(h.activeBuffer.X-1) != 0 {
				offset = 1
			}
			// if empty cell after fullwidth, need to go 2 cells back
			bufferRow.AddCodepointToCell(h.activeBuffer.X-offset, code, chWidth)
			for delta := chWidth - oldWidth; delta > 0; delta-- {
				bufferRow.SetCellFromCodepoint(h.activeBuffer.X, 0, 0, curAttr)
				h.activeBuffer.X++
			}
			continue
		}

		// insert mode: move characters to right
		if insertMode {
			bufferRow.InsertCells(h.activeBuffer.X, chWidth-oldWidth, h.activeBuffer.GetNullCell(curAttr))
			// a fullwidth char shifted into the last cell is lost
			if bufferRow.GetWidth(cols-1) == 2 {
				bufferRow.SetCellFromCodepoint(cols-1, NullCellCode, NullCellWidth, curAttr)
			}
		}

		// write current char to buffer and advance cursor
		bufferRow.SetCellFromCodepoint(h.activeBuffer.X, code, chWidth, curAttr)
		h.activeBuffer.X++

		// fullwidth char - also set next cell(s) to placeholder stubs
		if chWidth > 0 {
			for chWidth--; chWidth > 0; chWidth-- {
				bufferRow.SetCellFromCodepoint(h.activeBuffer.X, 0, 0, curAttr)
				h.activeBuffer.X++
			}
		}
	}

	h.parser.PrecedingJoinState = int(precedingJoinState)

	// handle wide chars: reset cell to the right if it is the second
	// cell of a wide char
	if h.activeBuffer.X < cols && end-start > 0 && bufferRow.GetWidth(h.activeBuffer.X) == 0 && !bufferRow.HasContent(h.activeBuffer.X) {
		bufferRow.SetCellFromCodepoint(h.activeBuffer.X, 0, 1, curAttr)
	}

	h.dirtyTracker.markDirty(h.activeBuffer.Y)
}

// RegisterCsiHandler forwards custom CSI handlers to the parser, with
// the window-options security gate for plain CSI t.
func (h *InputHandler) RegisterCsiHandler(id FunctionID, callback CsiHandler) {
	if id.Final == "t" && id.Prefix == "" && id.Intermediates == "" {
		h.parser.RegisterCsiHandler(id, func(params *Params) bool {
			if !paramToWindowOption(paramAt(params, 0), h.options.WindowOptions) {
				return true
			}
			return callback(params)
		})
		return
	}
	h.parser.RegisterCsiHandler(id, callback)
}

// RegisterDcsHandler forwards custom DCS handlers to the parser.
func (h *InputHandler) RegisterDcsHandler(id FunctionID, callback func(data string, params *Params) bool) {
	h.parser.RegisterDcsHandler(id, NewDcsHandler(callback))
}

// RegisterEscHandler forwards custom ESC handlers to the parser.
func (h *InputHandler) RegisterEscHandler(id FunctionID, callback EscHandler) {
	h.parser.RegisterEscHandler(id, callback)
}

// RegisterOscHandler forwards custom OSC handlers to the parser.
func (h *InputHandler) RegisterOscHandler(ident int, callback func(data string) bool) {
	h.parser.RegisterOscHandler(ident, NewOscHandler(callback))
}

// Bell rings the bell (BEL).
func (h *InputHandler) Bell() bool {
	if h.OnRequestBell != nil {
		h.OnRequestBell()
	}
	return true
}

// LineFeed handles LF/VT/FF.
func (h *InputHandler) LineFeed() bool {
	h.dirtyTracker.markDirty(h.activeBuffer.Y)
	if h.options.ConvertEol {
		h.activeBuffer.X = 0
	}
	h.activeBuffer.Y++
	if h.activeBuffer.Y == h.activeBuffer.ScrollBottom+1 {
		h.activeBuffer.Y--
		h.bufferService.Scroll(h.eraseAttrData(), false)
	} else if h.activeBuffer.Y >= h.bufferService.Rows {
		h.activeBuffer.Y = h.bufferService.Rows - 1
	} else {
		// an explicit line feed clears the wrapped state of the line
		h.activeBuffer.Lines.Get(h.activeBuffer.YBase + h.activeBuffer.Y).IsWrapped = false
	}
	// if the end of the line is hit, prevent wrapping to the next line
	if h.activeBuffer.X >= h.bufferService.Cols {
		h.activeBuffer.X--
	}
	h.dirtyTracker.markDirty(h.activeBuffer.Y)

	if h.OnLineFeed != nil {
		h.OnLineFeed()
	}
	return true
}

// CarriageReturn handles CR.
func (h *InputHandler) CarriageReturn() bool {
	h.activeBuffer.X = 0
	return true
}

// Backspace handles BS, honoring reverse wrap-around (CSI ? 45 h).
func (h *InputHandler) Backspace() bool {
	// reverse wrap-around is disabled
	if !h.coreService.DecPrivateModes.ReverseWraparound {
		h.restrictCursor(h.bufferService.Cols - 1)
		if h.activeBuffer.X > 0 {
			h.activeBuffer.X--
		}
		return true
	}

	// reverse wrap-around allows the cursor to be at x=cols to address
	// the last cell of a row by BS
	h.restrictCursor(h.bufferService.Cols)

	if h.activeBuffer.X > 0 {
		h.activeBuffer.X--
	} else {
		// reverse wrap-around: only previous soft NLs can be reversed
		// (isWrapped=true), only within scroll borders, cannot peek
		// into the scrollbuffer
		if h.activeBuffer.X == 0 &&
			h.activeBuffer.Y > h.activeBuffer.ScrollTop &&
			h.activeBuffer.Y <= h.activeBuffer.ScrollBottom &&
			h.activeBuffer.Lines.Get(h.activeBuffer.YBase+h.activeBuffer.Y).IsWrapped {
			h.activeBuffer.Lines.Get(h.activeBuffer.YBase + h.activeBuffer.Y).IsWrapped = false
			h.activeBuffer.Y--
			h.activeBuffer.X = h.bufferService.Cols - 1
			// find last taken cell - an empty cell with width 1 stems
			// from an early wrapping wide char, go one further back
			line := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + h.activeBuffer.Y)
			if line.HasWidth(h.activeBuffer.X) != 0 && !line.HasContent(h.activeBuffer.X) {
				h.activeBuffer.X--
			}
		}
	}
	h.restrictCursor(h.bufferService.Cols - 1)
	return true
}

// Tab handles HT.
func (h *InputHandler) Tab() bool {
	if h.activeBuffer.X >= h.bufferService.Cols {
		return true
	}
	originalX := h.activeBuffer.X
	h.activeBuffer.X = h.activeBuffer.NextStop(h.activeBuffer.X)
	if h.options.ScreenReaderMode && h.OnA11yTab != nil {
		h.OnA11yTab(h.activeBuffer.X - originalX)
	}
	return true
}

// ShiftOut handles SO (invoke G1).
func (h *InputHandler) ShiftOut() bool {
	h.charsetService.SetgLevel(1)
	return true
}

// ShiftIn handles SI (invoke G0).
func (h *InputHandler) ShiftIn() bool {
	h.charsetService.SetgLevel(0)
	return true
}

// restrictCursor restricts the cursor to viewport size / scroll margin
// (origin mode).
func (h *InputHandler) restrictCursor(maxCol int) {
	h.activeBuffer.X = min(maxCol, maxInt(0, h.activeBuffer.X))
	if h.coreService.DecPrivateModes.Origin {
		h.activeBuffer.Y = min(h.activeBuffer.ScrollBottom, maxInt(h.activeBuffer.ScrollTop, h.activeBuffer.Y))
	} else {
		h.activeBuffer.Y = min(h.bufferService.Rows-1, maxInt(0, h.activeBuffer.Y))
	}
	h.dirtyTracker.markDirty(h.activeBuffer.Y)
}

// setCursor sets the absolute cursor position.
func (h *InputHandler) setCursor(x, y int) {
	h.dirtyTracker.markDirty(h.activeBuffer.Y)
	if h.coreService.DecPrivateModes.Origin {
		h.activeBuffer.X = x
		h.activeBuffer.Y = h.activeBuffer.ScrollTop + y
	} else {
		h.activeBuffer.X = x
		h.activeBuffer.Y = y
	}
	h.restrictCursor(h.bufferService.Cols - 1)
	h.dirtyTracker.markDirty(h.activeBuffer.Y)
}

// moveCursor sets the relative cursor position.
func (h *InputHandler) moveCursor(x, y int) {
	h.restrictCursor(h.bufferService.Cols - 1)
	h.setCursor(h.activeBuffer.X+x, h.activeBuffer.Y+y)
}

// CursorUp handles CUU (stops at the top scroll margin).
func (h *InputHandler) CursorUp(params *Params) bool {
	diffToTop := h.activeBuffer.Y - h.activeBuffer.ScrollTop
	if diffToTop >= 0 {
		h.moveCursor(0, -min(diffToTop, paramOr1(params, 0)))
	} else {
		h.moveCursor(0, -paramOr1(params, 0))
	}
	return true
}

// CursorDown handles CUD (stops at the bottom scroll margin).
func (h *InputHandler) CursorDown(params *Params) bool {
	diffToBottom := h.activeBuffer.ScrollBottom - h.activeBuffer.Y
	if diffToBottom >= 0 {
		h.moveCursor(0, min(diffToBottom, paramOr1(params, 0)))
	} else {
		h.moveCursor(0, paramOr1(params, 0))
	}
	return true
}

// CursorForward handles CUF.
func (h *InputHandler) CursorForward(params *Params) bool {
	h.moveCursor(paramOr1(params, 0), 0)
	return true
}

// CursorBackward handles CUB.
func (h *InputHandler) CursorBackward(params *Params) bool {
	h.moveCursor(-paramOr1(params, 0), 0)
	return true
}

// CursorNextLine handles CNL.
func (h *InputHandler) CursorNextLine(params *Params) bool {
	h.CursorDown(params)
	h.activeBuffer.X = 0
	return true
}

// CursorPrecedingLine handles CPL.
func (h *InputHandler) CursorPrecedingLine(params *Params) bool {
	h.CursorUp(params)
	h.activeBuffer.X = 0
	return true
}

// CursorCharAbsolute handles CHA.
func (h *InputHandler) CursorCharAbsolute(params *Params) bool {
	h.setCursor(paramOr1(params, 0)-1, h.activeBuffer.Y)
	return true
}

// CursorPosition handles CUP.
func (h *InputHandler) CursorPosition(params *Params) bool {
	col := 0
	if params.Length >= 2 {
		col = paramOr1(params, 1) - 1
	}
	h.setCursor(col, paramOr1(params, 0)-1)
	return true
}

// CharPosAbsolute handles HPA.
func (h *InputHandler) CharPosAbsolute(params *Params) bool {
	h.setCursor(paramOr1(params, 0)-1, h.activeBuffer.Y)
	return true
}

// HPositionRelative handles HPR.
func (h *InputHandler) HPositionRelative(params *Params) bool {
	h.moveCursor(paramOr1(params, 0), 0)
	return true
}

// LinePosAbsolute handles VPA.
func (h *InputHandler) LinePosAbsolute(params *Params) bool {
	h.setCursor(h.activeBuffer.X, paramOr1(params, 0)-1)
	return true
}

// VPositionRelative handles VPR.
func (h *InputHandler) VPositionRelative(params *Params) bool {
	h.moveCursor(0, paramOr1(params, 0))
	return true
}

// HVPosition handles HVP (same as CUP).
func (h *InputHandler) HVPosition(params *Params) bool {
	h.CursorPosition(params)
	return true
}

// TabClear handles TBC.
func (h *InputHandler) TabClear(params *Params) bool {
	switch paramAt(params, 0) {
	case 0:
		delete(h.activeBuffer.Tabs, h.activeBuffer.X)
	case 3:
		h.activeBuffer.Tabs = map[int]bool{}
	}
	return true
}

// CursorForwardTab handles CHT.
func (h *InputHandler) CursorForwardTab(params *Params) bool {
	if h.activeBuffer.X >= h.bufferService.Cols {
		return true
	}
	for param := paramOr1(params, 0); param > 0; param-- {
		h.activeBuffer.X = h.activeBuffer.NextStop(h.activeBuffer.X)
	}
	return true
}

// CursorBackwardTab handles CBT.
func (h *InputHandler) CursorBackwardTab(params *Params) bool {
	if h.activeBuffer.X >= h.bufferService.Cols {
		return true
	}
	for param := paramOr1(params, 0); param > 0; param-- {
		h.activeBuffer.X = h.activeBuffer.PrevStop(h.activeBuffer.X)
	}
	return true
}

// SelectProtected handles DECSCA.
func (h *InputHandler) SelectProtected(params *Params) bool {
	p := paramAt(params, 0)
	if p == 1 {
		h.curAttrData.Bg |= BgProtected
	}
	if p == 2 || p == 0 {
		h.curAttrData.Bg &= ^BgProtected
	}
	return true
}

// eraseInBufferLine erases cells in a row; the cells get replaced with
// the eraseChar of the terminal.
func (h *InputHandler) eraseInBufferLine(y, start, end int, clearWrap, respectProtect bool) {
	line := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + y)
	line.ReplaceCells(start, end, h.activeBuffer.GetNullCell(h.eraseAttrData()), respectProtect)
	if clearWrap {
		line.IsWrapped = false
	}
}

// resetBufferLine resets a whole row and clears its wrapped state.
func (h *InputHandler) resetBufferLine(y int, respectProtect bool) {
	if h.activeBuffer.YBase+y >= h.activeBuffer.Lines.Length() {
		return
	}
	line := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + y)
	line.Fill(h.activeBuffer.GetNullCell(h.eraseAttrData()), respectProtect)
	h.bufferService.Buffer().ClearMarkers(h.activeBuffer.YBase + y)
	line.IsWrapped = false
}

// EraseInDisplay handles ED and DECSED.
func (h *InputHandler) EraseInDisplay(params *Params, respectProtect bool) bool {
	h.restrictCursor(h.bufferService.Cols)
	switch paramAt(params, 0) {
	case 0:
		j := h.activeBuffer.Y
		h.dirtyTracker.markDirty(j)
		h.eraseInBufferLine(j, h.activeBuffer.X, h.bufferService.Cols, h.activeBuffer.X == 0, respectProtect)
		for j++; j < h.bufferService.Rows; j++ {
			h.resetBufferLine(j, respectProtect)
		}
		h.dirtyTracker.markDirty(j)
	case 1:
		j := h.activeBuffer.Y
		h.dirtyTracker.markDirty(j)
		// deleted front part of line and everything before; this line
		// will no longer be wrapped
		h.eraseInBufferLine(j, 0, h.activeBuffer.X+1, true, respectProtect)
		if h.activeBuffer.X+1 >= h.bufferService.Cols {
			// deleted entire previous line; the next line can no
			// longer be wrapped (note: faithful to the JS which
			// indexes lines without ybase here)
			if j+1 < h.activeBuffer.Lines.Length() {
				h.activeBuffer.Lines.Get(j + 1).IsWrapped = false
			}
		}
		for j--; j >= 0; j-- {
			h.resetBufferLine(j, respectProtect)
		}
		h.dirtyTracker.markDirty(0)
	case 2:
		if h.options.ScrollOnEraseInDisplay {
			j := h.bufferService.Rows
			h.dirtyTracker.markRangeDirty(0, j-1)
			for j--; j >= 0; j-- {
				line := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + j)
				if line.GetTrimmedLength() > 0 {
					break
				}
			}
			for ; j >= 0; j-- {
				h.bufferService.Scroll(h.eraseAttrData(), false)
			}
		} else {
			j := h.bufferService.Rows
			h.dirtyTracker.markDirty(j - 1)
			for j--; j >= 0; j-- {
				h.resetBufferLine(j, respectProtect)
			}
			h.dirtyTracker.markDirty(0)
		}
	case 3:
		// clear scrollback (everything not in viewport)
		scrollBackSize := h.activeBuffer.Lines.Length() - h.bufferService.Rows
		if scrollBackSize > 0 {
			h.activeBuffer.Lines.TrimStart(scrollBackSize)
			h.activeBuffer.YBase = maxInt(h.activeBuffer.YBase-scrollBackSize, 0)
			h.activeBuffer.YDisp = maxInt(h.activeBuffer.YDisp-scrollBackSize, 0)
			// force a scroll event to refresh viewport
			if h.OnScroll != nil {
				h.OnScroll(0)
			}
		}
	}
	return true
}

// EraseInLine handles EL and DECSEL.
func (h *InputHandler) EraseInLine(params *Params, respectProtect bool) bool {
	h.restrictCursor(h.bufferService.Cols)
	switch paramAt(params, 0) {
	case 0:
		h.eraseInBufferLine(h.activeBuffer.Y, h.activeBuffer.X, h.bufferService.Cols, h.activeBuffer.X == 0, respectProtect)
	case 1:
		h.eraseInBufferLine(h.activeBuffer.Y, 0, h.activeBuffer.X+1, false, respectProtect)
	case 2:
		h.eraseInBufferLine(h.activeBuffer.Y, 0, h.bufferService.Cols, true, respectProtect)
	}
	h.dirtyTracker.markDirty(h.activeBuffer.Y)
	return true
}

// InsertLines handles IL.
func (h *InputHandler) InsertLines(params *Params) bool {
	h.restrictCursor(h.bufferService.Cols - 1)
	param := paramOr1(params, 0)

	if h.activeBuffer.Y > h.activeBuffer.ScrollBottom || h.activeBuffer.Y < h.activeBuffer.ScrollTop {
		return true
	}

	row := h.activeBuffer.YBase + h.activeBuffer.Y

	scrollBottomRowsOffset := h.bufferService.Rows - 1 - h.activeBuffer.ScrollBottom
	scrollBottomAbsolute := h.bufferService.Rows - 1 + h.activeBuffer.YBase - scrollBottomRowsOffset + 1
	for ; param > 0; param-- {
		// blankLine(true) - xterm/linux behavior
		h.activeBuffer.Lines.Splice(scrollBottomAbsolute-1, 1)
		h.activeBuffer.Lines.Splice(row, 0, h.activeBuffer.GetBlankLine(h.eraseAttrData(), false))
	}

	h.dirtyTracker.markRangeDirty(h.activeBuffer.Y, h.activeBuffer.ScrollBottom)
	h.activeBuffer.X = 0 // see https://vt100.net/docs/vt220-rm/chapter4.html - vt220 only?
	return true
}

// DeleteLines handles DL.
func (h *InputHandler) DeleteLines(params *Params) bool {
	h.restrictCursor(h.bufferService.Cols - 1)
	param := paramOr1(params, 0)

	if h.activeBuffer.Y > h.activeBuffer.ScrollBottom || h.activeBuffer.Y < h.activeBuffer.ScrollTop {
		return true
	}

	row := h.activeBuffer.YBase + h.activeBuffer.Y

	j := h.bufferService.Rows - 1 - h.activeBuffer.ScrollBottom
	j = h.bufferService.Rows - 1 + h.activeBuffer.YBase - j
	for ; param > 0; param-- {
		// blankLine(true) - xterm/linux behavior
		h.activeBuffer.Lines.Splice(row, 1)
		h.activeBuffer.Lines.Splice(j, 0, h.activeBuffer.GetBlankLine(h.eraseAttrData(), false))
	}

	h.dirtyTracker.markRangeDirty(h.activeBuffer.Y, h.activeBuffer.ScrollBottom)
	h.activeBuffer.X = 0
	return true
}

// InsertChars handles ICH.
func (h *InputHandler) InsertChars(params *Params) bool {
	h.restrictCursor(h.bufferService.Cols - 1)
	line := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + h.activeBuffer.Y)
	if line != nil {
		line.InsertCells(h.activeBuffer.X, paramOr1(params, 0), h.activeBuffer.GetNullCell(h.eraseAttrData()))
		h.dirtyTracker.markDirty(h.activeBuffer.Y)
	}
	return true
}

// DeleteChars handles DCH.
func (h *InputHandler) DeleteChars(params *Params) bool {
	h.restrictCursor(h.bufferService.Cols - 1)
	line := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + h.activeBuffer.Y)
	if line != nil {
		line.DeleteCells(h.activeBuffer.X, paramOr1(params, 0), h.activeBuffer.GetNullCell(h.eraseAttrData()))
		h.dirtyTracker.markDirty(h.activeBuffer.Y)
	}
	return true
}

// ScrollUp handles SU.
func (h *InputHandler) ScrollUp(params *Params) bool {
	for param := paramOr1(params, 0); param > 0; param-- {
		h.activeBuffer.Lines.Splice(h.activeBuffer.YBase+h.activeBuffer.ScrollTop, 1)
		h.activeBuffer.Lines.Splice(h.activeBuffer.YBase+h.activeBuffer.ScrollBottom, 0, h.activeBuffer.GetBlankLine(h.eraseAttrData(), false))
	}
	h.dirtyTracker.markRangeDirty(h.activeBuffer.ScrollTop, h.activeBuffer.ScrollBottom)
	return true
}

// ScrollDown handles SD.
func (h *InputHandler) ScrollDown(params *Params) bool {
	for param := paramOr1(params, 0); param > 0; param-- {
		h.activeBuffer.Lines.Splice(h.activeBuffer.YBase+h.activeBuffer.ScrollBottom, 1)
		h.activeBuffer.Lines.Splice(h.activeBuffer.YBase+h.activeBuffer.ScrollTop, 0, h.activeBuffer.GetBlankLine(NewAttributeData(), false))
	}
	h.dirtyTracker.markRangeDirty(h.activeBuffer.ScrollTop, h.activeBuffer.ScrollBottom)
	return true
}

// ScrollLeft handles SL (ECMA-48).
func (h *InputHandler) ScrollLeft(params *Params) bool {
	if h.activeBuffer.Y > h.activeBuffer.ScrollBottom || h.activeBuffer.Y < h.activeBuffer.ScrollTop {
		return true
	}
	param := paramOr1(params, 0)
	for y := h.activeBuffer.ScrollTop; y <= h.activeBuffer.ScrollBottom; y++ {
		line := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + y)
		line.DeleteCells(0, param, h.activeBuffer.GetNullCell(h.eraseAttrData()))
		line.IsWrapped = false
	}
	h.dirtyTracker.markRangeDirty(h.activeBuffer.ScrollTop, h.activeBuffer.ScrollBottom)
	return true
}

// ScrollRight handles SR (ECMA-48).
func (h *InputHandler) ScrollRight(params *Params) bool {
	if h.activeBuffer.Y > h.activeBuffer.ScrollBottom || h.activeBuffer.Y < h.activeBuffer.ScrollTop {
		return true
	}
	param := paramOr1(params, 0)
	for y := h.activeBuffer.ScrollTop; y <= h.activeBuffer.ScrollBottom; y++ {
		line := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + y)
		line.InsertCells(0, param, h.activeBuffer.GetNullCell(h.eraseAttrData()))
		line.IsWrapped = false
	}
	h.dirtyTracker.markRangeDirty(h.activeBuffer.ScrollTop, h.activeBuffer.ScrollBottom)
	return true
}

// InsertColumns handles DECIC.
func (h *InputHandler) InsertColumns(params *Params) bool {
	if h.activeBuffer.Y > h.activeBuffer.ScrollBottom || h.activeBuffer.Y < h.activeBuffer.ScrollTop {
		return true
	}
	param := paramOr1(params, 0)
	for y := h.activeBuffer.ScrollTop; y <= h.activeBuffer.ScrollBottom; y++ {
		line := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + y)
		line.InsertCells(h.activeBuffer.X, param, h.activeBuffer.GetNullCell(h.eraseAttrData()))
		line.IsWrapped = false
	}
	h.dirtyTracker.markRangeDirty(h.activeBuffer.ScrollTop, h.activeBuffer.ScrollBottom)
	return true
}

// DeleteColumns handles DECDC.
func (h *InputHandler) DeleteColumns(params *Params) bool {
	if h.activeBuffer.Y > h.activeBuffer.ScrollBottom || h.activeBuffer.Y < h.activeBuffer.ScrollTop {
		return true
	}
	param := paramOr1(params, 0)
	for y := h.activeBuffer.ScrollTop; y <= h.activeBuffer.ScrollBottom; y++ {
		line := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + y)
		line.DeleteCells(h.activeBuffer.X, param, h.activeBuffer.GetNullCell(h.eraseAttrData()))
		line.IsWrapped = false
	}
	h.dirtyTracker.markRangeDirty(h.activeBuffer.ScrollTop, h.activeBuffer.ScrollBottom)
	return true
}

// EraseChars handles ECH.
func (h *InputHandler) EraseChars(params *Params) bool {
	h.restrictCursor(h.bufferService.Cols - 1)
	line := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + h.activeBuffer.Y)
	if line != nil {
		line.ReplaceCells(h.activeBuffer.X, h.activeBuffer.X+paramOr1(params, 0), h.activeBuffer.GetNullCell(h.eraseAttrData()), false)
		h.dirtyTracker.markDirty(h.activeBuffer.Y)
	}
	return true
}

// RepeatPrecedingCharacter handles REP; repeats entire grapheme
// clusters like the original's extension of xterm's behavior.
func (h *InputHandler) RepeatPrecedingCharacter(params *Params) bool {
	joinState := uint32(h.parser.PrecedingJoinState) // #nosec G115 -- Unicode codepoints and the parser's join state
	if joinState == 0 {
		return true
	}
	// call print to insert the chars and handle correct wrapping
	length := paramOr1(params, 0)
	chWidth := ExtractWidth(joinState)
	x := h.activeBuffer.X - chWidth
	bufferRow := h.activeBuffer.Lines.Get(h.activeBuffer.YBase + h.activeBuffer.Y)
	text := bufferRow.GetString(x)
	var codepoints []uint32
	for _, r := range text {
		codepoints = append(codepoints, uint32(r)) // #nosec G115 -- Unicode codepoints and the parser's join state
	}
	data := make([]uint32, 0, len(codepoints)*length)
	for i := 0; i < length; i++ {
		data = append(data, codepoints...)
	}
	h.Print(data, 0, len(data))
	return true
}

// SendDeviceAttributesPrimary handles DA1.
func (h *InputHandler) SendDeviceAttributesPrimary(params *Params) bool {
	if paramAt(params, 0) > 0 {
		return true
	}
	if h.is("xterm") || h.is("rxvt-unicode") || h.is("screen") {
		h.coreService.TriggerDataEvent(c0ESC+"[?1;2c", false)
	} else if h.is("linux") {
		h.coreService.TriggerDataEvent(c0ESC+"[?6c", false)
	}
	return true
}

// SendDeviceAttributesSecondary handles DA2.
func (h *InputHandler) SendDeviceAttributesSecondary(params *Params) bool {
	if paramAt(params, 0) > 0 {
		return true
	}
	if h.is("xterm") {
		h.coreService.TriggerDataEvent(c0ESC+"[>0;276;0c", false)
	} else if h.is("rxvt-unicode") {
		h.coreService.TriggerDataEvent(c0ESC+"[>85;95;0c", false)
	} else if h.is("linux") {
		// linux console echoes parameters
		h.coreService.TriggerDataEvent(strconv.Itoa(paramAt(params, 0))+"c", false)
	} else if h.is("screen") {
		h.coreService.TriggerDataEvent(c0ESC+"[>83;40003;0c", false)
	}
	return true
}

func (h *InputHandler) is(term string) bool {
	return strings.HasPrefix(h.options.TermName, term)
}

// SetMode handles SM (only IRM and LNM are supported).
func (h *InputHandler) SetMode(params *Params) bool {
	for i := 0; i < params.Length; i++ {
		switch paramAt(params, i) {
		case 4:
			h.coreService.Modes.InsertMode = true
		case 20:
			h.options.ConvertEol = true
		}
	}
	return true
}

// SetModePrivate handles DECSET.
func (h *InputHandler) SetModePrivate(params *Params) bool {
	for i := 0; i < params.Length; i++ {
		switch paramAt(params, i) {
		case 1:
			h.coreService.DecPrivateModes.ApplicationCursorKeys = true
		case 2:
			h.charsetService.SetgCharset(0, DefaultCharset)
			h.charsetService.SetgCharset(1, DefaultCharset)
			h.charsetService.SetgCharset(2, DefaultCharset)
			h.charsetService.SetgCharset(3, DefaultCharset)
		case 3:
			// DECCOLM - only active with 'SetWinLines' (24) enabled
			if h.options.WindowOptions.SetWinLines {
				h.bufferService.Resize(132, h.bufferService.Rows)
				if h.OnRequestReset != nil {
					h.OnRequestReset()
				}
			}
		case 6:
			h.coreService.DecPrivateModes.Origin = true
			h.setCursor(0, 0)
		case 7:
			h.coreService.DecPrivateModes.Wraparound = true
		case 12:
			h.options.CursorBlink = true
		case 45:
			h.coreService.DecPrivateModes.ReverseWraparound = true
		case 66:
			h.coreService.DecPrivateModes.ApplicationKeypad = true
			if h.OnRequestSyncScrollBar != nil {
				h.OnRequestSyncScrollBar()
			}
		case 9: // X10 Mouse - no release, no motion, no wheel, no modifiers
			h.mouseService.SetActiveProtocol("X10")
		case 1000: // vt200 mouse - no motion
			h.mouseService.SetActiveProtocol("VT200")
		case 1002: // button event mouse
			h.mouseService.SetActiveProtocol("DRAG")
		case 1003: // any event mouse
			h.mouseService.SetActiveProtocol("ANY")
		case 1004: // send focusin/focusout events
			h.coreService.DecPrivateModes.SendFocus = true
			if h.OnRequestSendFocus != nil {
				h.OnRequestSendFocus()
			}
		case 1005: // utf8 ext mode mouse - removed in xterm.js #2507
		case 1006: // sgr ext mode mouse
			h.mouseService.SetActiveEncoding("SGR")
		case 1015: // urxvt ext mode mouse - removed in xterm.js #2507
		case 1016: // sgr pixels mode mouse
			h.mouseService.SetActiveEncoding("SGR_PIXELS")
		case 25: // show cursor
			h.coreService.IsCursorHidden = false
		case 1048: // alt screen cursor
			h.SaveCursor()
		case 1049: // alt screen buffer cursor
			h.SaveCursor()
			fallthrough
		case 47, 1047: // alt screen buffer
			h.bufferService.Buffers.ActivateAltBuffer(h.eraseAttrData())
			h.coreService.IsCursorInitialized = true
			if h.OnRequestRefreshRows != nil {
				h.OnRequestRefreshRows(-1, -1)
			}
			if h.OnRequestSyncScrollBar != nil {
				h.OnRequestSyncScrollBar()
			}
		case 2004: // bracketed paste mode
			h.coreService.DecPrivateModes.BracketedPasteMode = true
		case 2026: // synchronized output
			h.coreService.DecPrivateModes.SynchronizedOutput = true
		}
	}
	return true
}

// ResetMode handles RM.
func (h *InputHandler) ResetMode(params *Params) bool {
	for i := 0; i < params.Length; i++ {
		switch paramAt(params, i) {
		case 4:
			h.coreService.Modes.InsertMode = false
		case 20:
			h.options.ConvertEol = false
		}
	}
	return true
}

// ResetModePrivate handles DECRST.
func (h *InputHandler) ResetModePrivate(params *Params) bool {
	for i := 0; i < params.Length; i++ {
		switch paramAt(params, i) {
		case 1:
			h.coreService.DecPrivateModes.ApplicationCursorKeys = false
		case 3:
			if h.options.WindowOptions.SetWinLines {
				h.bufferService.Resize(80, h.bufferService.Rows)
				if h.OnRequestReset != nil {
					h.OnRequestReset()
				}
			}
		case 6:
			h.coreService.DecPrivateModes.Origin = false
			h.setCursor(0, 0)
		case 7:
			h.coreService.DecPrivateModes.Wraparound = false
		case 12:
			h.options.CursorBlink = false
		case 45:
			h.coreService.DecPrivateModes.ReverseWraparound = false
		case 66:
			h.coreService.DecPrivateModes.ApplicationKeypad = false
			if h.OnRequestSyncScrollBar != nil {
				h.OnRequestSyncScrollBar()
			}
		case 9, 1000, 1002, 1003:
			h.mouseService.SetActiveProtocol("NONE")
		case 1004:
			h.coreService.DecPrivateModes.SendFocus = false
		case 1005: // removed in xterm.js #2507
		case 1006:
			h.mouseService.SetActiveEncoding("DEFAULT")
		case 1015: // removed in xterm.js #2507
		case 1016:
			h.mouseService.SetActiveEncoding("DEFAULT")
		case 25: // hide cursor
			h.coreService.IsCursorHidden = true
		case 1048: // alt screen cursor
			h.RestoreCursor()
		case 1049, 47, 1047: // normal screen buffer
			h.bufferService.Buffers.ActivateNormalBuffer()
			if paramAt(params, i) == 1049 {
				h.RestoreCursor()
			}
			h.coreService.IsCursorInitialized = true
			if h.OnRequestRefreshRows != nil {
				h.OnRequestRefreshRows(-1, -1)
			}
			if h.OnRequestSyncScrollBar != nil {
				h.OnRequestSyncScrollBar()
			}
		case 2004:
			h.coreService.DecPrivateModes.BracketedPasteMode = false
		case 2026:
			h.coreService.DecPrivateModes.SynchronizedOutput = false
			if h.OnRequestRefreshRows != nil {
				h.OnRequestRefreshRows(-1, -1)
			}
		}
	}
	return true
}

// DECRPM mode values.
const (
	modeNotRecognized    = 0
	modeSet              = 1
	modeReset            = 2
	modePermanentlySet   = 3
	modePermanentlyReset = 4
)

// RequestMode handles DECRQM (reports via DECRPM).
func (h *InputHandler) RequestMode(params *Params, ansi bool) bool {
	dm := &h.coreService.DecPrivateModes
	mouseProtocol := h.mouseService.ActiveProtocol()
	mouseEncoding := h.mouseService.ActiveEncoding()

	f := func(m, v int) bool {
		q := "?"
		if ansi {
			q = ""
		}
		h.coreService.TriggerDataEvent(fmt.Sprintf("%s[%s%d;%d$y", c0ESC, q, m, v), false)
		return true
	}
	b2v := func(value bool) int {
		if value {
			return modeSet
		}
		return modeReset
	}

	p := paramAt(params, 0)

	if ansi {
		switch p {
		case 2:
			return f(p, modePermanentlyReset)
		case 4:
			return f(p, b2v(h.coreService.Modes.InsertMode))
		case 12:
			return f(p, modePermanentlySet)
		case 20:
			return f(p, b2v(h.options.ConvertEol))
		}
		return f(p, modeNotRecognized)
	}

	switch p {
	case 1:
		return f(p, b2v(dm.ApplicationCursorKeys))
	case 3:
		if h.options.WindowOptions.SetWinLines {
			switch h.bufferService.Cols {
			case 80:
				return f(p, modeReset)
			case 132:
				return f(p, modeSet)
			}
		}
		return f(p, modeNotRecognized)
	case 6:
		return f(p, b2v(dm.Origin))
	case 7:
		return f(p, b2v(dm.Wraparound))
	case 8:
		return f(p, modePermanentlySet)
	case 9:
		return f(p, b2v(mouseProtocol == "X10"))
	case 12:
		return f(p, b2v(h.options.CursorBlink))
	case 25:
		return f(p, b2v(!h.coreService.IsCursorHidden))
	case 45:
		return f(p, b2v(dm.ReverseWraparound))
	case 66:
		return f(p, b2v(dm.ApplicationKeypad))
	case 67:
		return f(p, modePermanentlyReset)
	case 1000:
		return f(p, b2v(mouseProtocol == "VT200"))
	case 1002:
		return f(p, b2v(mouseProtocol == "DRAG"))
	case 1003:
		return f(p, b2v(mouseProtocol == "ANY"))
	case 1004:
		return f(p, b2v(dm.SendFocus))
	case 1005:
		return f(p, modePermanentlyReset)
	case 1006:
		return f(p, b2v(mouseEncoding == "SGR"))
	case 1015:
		return f(p, modePermanentlyReset)
	case 1016:
		return f(p, b2v(mouseEncoding == "SGR_PIXELS"))
	case 1048:
		return f(p, modeSet) // xterm always returns SET here
	case 47, 1047, 1049:
		return f(p, b2v(h.bufferService.Buffers.Active() == h.bufferService.Buffers.Alt()))
	case 2004:
		return f(p, b2v(dm.BracketedPasteMode))
	case 2026:
		return f(p, b2v(dm.SynchronizedOutput))
	}
	return f(p, modeNotRecognized)
}

// updateAttrColor writes color information packed with the color mode.
func updateAttrColor(color uint32, mode, c1, c2, c3 int) uint32 {
	switch mode {
	case 2:
		color |= AttrCMRGB
		color &= ^AttrRGBMask
		color |= FromColorRGB([3]int{c1, c2, c3})
	case 5:
		color &= ^(AttrCMMask | AttrPColorMask)
		color |= AttrCMP256 | uint32(c1&0xff)
	}
	return color
}

// extractColor extracts and applies color params/subparams of SGR
// 38/48/58; returns the advance for the params index.
func (h *InputHandler) extractColor(params *Params, pos int, attr *AttributeData) int {
	// normalize params: [target, CM, ign, val, val, val]
	accu := [6]int{0, 0, -1, 0, 0, 0}
	// alignment placeholder for non color space sequences
	cSpace := 0
	// advance we took in params
	advance := 0

	for {
		accu[advance+cSpace] = paramAt(params, pos+advance)
		if params.HasSubParams(pos + advance) {
			subparams := params.GetSubParams(pos + advance)
			i := 0
			for {
				if accu[1] == 5 {
					cSpace = 1
				}
				if idx := advance + i + 1 + cSpace; idx < len(accu) {
					accu[idx] = int(subparams[i])
				}
				i++
				if i >= len(subparams) || i+advance+1+cSpace >= len(accu) {
					break
				}
			}
			break
		}
		// exit early if we can decide the color mode with semicolons
		if (accu[1] == 5 && advance+cSpace >= 2) ||
			(accu[1] == 2 && advance+cSpace >= 5) {
			break
		}
		// offset colorSpace slot for semicolon mode
		if accu[1] != 0 {
			cSpace = 1
		}
		advance++
		if advance+pos >= params.Length || advance+cSpace >= len(accu) {
			break
		}
	}

	// set default values to 0
	for i := 2; i < len(accu); i++ {
		if accu[i] == -1 {
			accu[i] = 0
		}
	}

	// apply colors
	switch accu[0] {
	case 38:
		attr.Fg = updateAttrColor(attr.Fg, accu[1], accu[3], accu[4], accu[5])
	case 48:
		attr.Bg = updateAttrColor(attr.Bg, accu[1], accu[3], accu[4], accu[5])
	case 58:
		attr.Extended = attr.Extended.Clone()
		attr.Extended.SetUnderlineColor(updateAttrColor(attr.Extended.UnderlineColor(), accu[1], accu[3], accu[4], accu[5]))
	}

	return advance
}

// processUnderline applies SGR 4 subparam styles.
func (h *InputHandler) processUnderline(style int, attr *AttributeData) {
	// treat extended attrs as immutable, always clone
	attr.Extended = attr.Extended.Clone()

	// default to 1 == single underline
	if style == -1 || style > 5 {
		style = 1
	}
	attr.Extended.SetUnderlineStyle(style)
	attr.Fg |= FgUnderline

	// 0 deactivates underline
	if style == 0 {
		attr.Fg &= ^FgUnderline
	}

	attr.UpdateExtended()
}

func (h *InputHandler) processSGR0(attr *AttributeData) {
	attr.Fg = 0 // DEFAULT_ATTR_DATA fg
	attr.Bg = 0 // DEFAULT_ATTR_DATA bg
	attr.Extended = attr.Extended.Clone()
	// reset underline style and color, but not e.g. the url id
	attr.Extended.SetUnderlineStyle(UnderlineNone)
	attr.Extended.SetUnderlineColor(0)
	attr.UpdateExtended()
}

// CharAttributes handles SGR.
func (h *InputHandler) CharAttributes(params *Params) bool {
	// optimize a single SGR0
	if params.Length == 1 && paramAt(params, 0) == 0 {
		h.processSGR0(h.curAttrData)
		return true
	}

	l := params.Length
	attr := h.curAttrData

	for i := 0; i < l; i++ {
		p := paramAt(params, i)
		switch {
		case p >= 30 && p <= 37:
			// fg color 8
			attr.Fg &= ^(AttrCMMask | AttrPColorMask)
			attr.Fg |= AttrCMP16 | uint32(p-30)
		case p >= 40 && p <= 47:
			// bg color 8
			attr.Bg &= ^(AttrCMMask | AttrPColorMask)
			attr.Bg |= AttrCMP16 | uint32(p-40)
		case p >= 90 && p <= 97:
			// fg color 16
			attr.Fg &= ^(AttrCMMask | AttrPColorMask)
			attr.Fg |= AttrCMP16 | uint32(p-90) | 8
		case p >= 100 && p <= 107:
			// bg color 16
			attr.Bg &= ^(AttrCMMask | AttrPColorMask)
			attr.Bg |= AttrCMP16 | uint32(p-100) | 8
		case p == 0:
			h.processSGR0(attr)
		case p == 1:
			attr.Fg |= FgBold
		case p == 3:
			attr.Bg |= BgItalic
		case p == 4:
			attr.Fg |= FgUnderline
			style := UnderlineSingle
			if params.HasSubParams(i) {
				style = int(params.GetSubParams(i)[0])
			}
			h.processUnderline(style, attr)
		case p == 5:
			attr.Fg |= FgBlink
		case p == 7:
			attr.Fg |= FgInverse
		case p == 8:
			attr.Fg |= FgInvisible
		case p == 9:
			attr.Fg |= FgStrikethrough
		case p == 2:
			attr.Bg |= BgDim
		case p == 21:
			h.processUnderline(UnderlineDouble, attr)
		case p == 22:
			attr.Fg &= ^FgBold
			attr.Bg &= ^BgDim
		case p == 23:
			attr.Bg &= ^BgItalic
		case p == 24:
			attr.Fg &= ^FgUnderline
			h.processUnderline(UnderlineNone, attr)
		case p == 25:
			attr.Fg &= ^FgBlink
		case p == 27:
			attr.Fg &= ^FgInverse
		case p == 28:
			attr.Fg &= ^FgInvisible
		case p == 29:
			attr.Fg &= ^FgStrikethrough
		case p == 39:
			// reset fg (default fg/bg are 0, so just clear)
			attr.Fg &= ^(AttrCMMask | AttrRGBMask)
		case p == 49:
			attr.Bg &= ^(AttrCMMask | AttrRGBMask)
		case p == 38 || p == 48 || p == 58:
			i += h.extractColor(params, i, attr)
		case p == 53:
			attr.Bg |= BgOverline
		case p == 55:
			attr.Bg &= ^BgOverline
		case p == 59:
			attr.Extended = attr.Extended.Clone()
			attr.Extended.SetUnderlineColor(^uint32(0)) // -1: default color
			attr.UpdateExtended()
		}
	}
	return true
}

// DeviceStatus handles DSR.
func (h *InputHandler) DeviceStatus(params *Params) bool {
	switch paramAt(params, 0) {
	case 5:
		h.coreService.TriggerDataEvent(c0ESC+"[0n", false)
	case 6:
		h.coreService.TriggerDataEvent(fmt.Sprintf("%s[%d;%dR", c0ESC, h.activeBuffer.Y+1, h.activeBuffer.X+1), false)
	}
	return true
}

// DeviceStatusPrivate handles DEC-specific DSR (only CPR).
func (h *InputHandler) DeviceStatusPrivate(params *Params) bool {
	if paramAt(params, 0) == 6 {
		h.coreService.TriggerDataEvent(fmt.Sprintf("%s[?%d;%dR", c0ESC, h.activeBuffer.Y+1, h.activeBuffer.X+1), false)
	}
	return true
}

// SoftReset handles DECSTR.
func (h *InputHandler) SoftReset(params *Params) bool {
	h.coreService.IsCursorHidden = false
	if h.OnRequestSyncScrollBar != nil {
		h.OnRequestSyncScrollBar()
	}
	h.activeBuffer.ScrollTop = 0
	h.activeBuffer.ScrollBottom = h.bufferService.Rows - 1
	h.curAttrData = NewAttributeData()
	h.coreService.Reset()
	h.charsetService.Reset()

	// reset DECSC data
	h.activeBuffer.SavedX = 0
	h.activeBuffer.SavedY = h.activeBuffer.YBase
	h.activeBuffer.SavedCurAttrData.Fg = h.curAttrData.Fg
	h.activeBuffer.SavedCurAttrData.Bg = h.curAttrData.Bg
	h.activeBuffer.SavedCharset = h.charsetService.Charset

	// reset DECOM
	h.coreService.DecPrivateModes.Origin = false
	return true
}

// SetCursorStyle handles DECSCUSR.
func (h *InputHandler) SetCursorStyle(params *Params) bool {
	param := 1
	if params.Length != 0 {
		param = paramAt(params, 0)
	}
	if param == 0 {
		h.coreService.DecPrivateModes.CursorStyle = nil
		h.coreService.DecPrivateModes.CursorBlink = nil
	} else {
		var style string
		switch param {
		case 1, 2:
			style = "block"
		case 3, 4:
			style = "underline"
		case 5, 6:
			style = "bar"
		}
		if style != "" {
			h.coreService.DecPrivateModes.CursorStyle = &style
		}
		isBlinking := param%2 == 1
		h.coreService.DecPrivateModes.CursorBlink = &isBlinking
	}
	return true
}

// SetScrollRegion handles DECSTBM.
func (h *InputHandler) SetScrollRegion(params *Params) bool {
	top := paramOr1(params, 0)
	bottom := h.bufferService.Rows
	if params.Length >= 2 {
		if b := paramAt(params, 1); b != 0 && b <= h.bufferService.Rows {
			bottom = b
		}
	}

	if bottom > top {
		h.activeBuffer.ScrollTop = top - 1
		h.activeBuffer.ScrollBottom = bottom - 1
		h.setCursor(0, 0)
	}
	return true
}

// WindowOptionsHandler handles CSI t window manipulations (gated by
// Options.WindowOptions).
func (h *InputHandler) WindowOptionsHandler(params *Params) bool {
	if !paramToWindowOption(paramAt(params, 0), h.options.WindowOptions) {
		return true
	}
	second := 0
	if params.Length > 1 {
		second = paramAt(params, 1)
	}
	switch paramAt(params, 0) {
	case 14: // GetWinSizePixels, returns CSI 4 ; height ; width t
		if second != 2 && h.OnRequestWindowsOptionsReport != nil {
			h.OnRequestWindowsOptionsReport(ReportWinSizePixels)
		}
	case 16: // GetCellSizePixels, returns CSI 6 ; height ; width t
		if h.OnRequestWindowsOptionsReport != nil {
			h.OnRequestWindowsOptionsReport(ReportCellSizePixels)
		}
	case 18: // GetWinSizeChars, returns CSI 8 ; height ; width t
		h.coreService.TriggerDataEvent(fmt.Sprintf("%s[8;%d;%dt", c0ESC, h.bufferService.Rows, h.bufferService.Cols), false)
	case 22: // PushTitle
		if second == 0 || second == 2 {
			h.windowTitleStack = append(h.windowTitleStack, h.windowTitle)
			if len(h.windowTitleStack) > titleStackLimit {
				h.windowTitleStack = h.windowTitleStack[1:]
			}
		}
		if second == 0 || second == 1 {
			h.iconNameStack = append(h.iconNameStack, h.iconName)
			if len(h.iconNameStack) > titleStackLimit {
				h.iconNameStack = h.iconNameStack[1:]
			}
		}
	case 23: // PopTitle
		if second == 0 || second == 2 {
			if n := len(h.windowTitleStack); n > 0 {
				h.SetTitle(h.windowTitleStack[n-1])
				h.windowTitleStack = h.windowTitleStack[:n-1]
			}
		}
		if second == 0 || second == 1 {
			if n := len(h.iconNameStack); n > 0 {
				h.SetIconName(h.iconNameStack[n-1])
				h.iconNameStack = h.iconNameStack[:n-1]
			}
		}
	}
	return true
}

// SaveCursor handles DECSC / CSI s.
func (h *InputHandler) SaveCursor() bool {
	h.activeBuffer.SavedX = h.activeBuffer.X
	h.activeBuffer.SavedY = h.activeBuffer.YBase + h.activeBuffer.Y
	h.activeBuffer.SavedCurAttrData.Fg = h.curAttrData.Fg
	h.activeBuffer.SavedCurAttrData.Bg = h.curAttrData.Bg
	h.activeBuffer.SavedCharset = h.charsetService.Charset
	return true
}

// RestoreCursor handles DECRC / CSI u.
func (h *InputHandler) RestoreCursor() bool {
	h.activeBuffer.X = h.activeBuffer.SavedX
	h.activeBuffer.Y = maxInt(h.activeBuffer.SavedY-h.activeBuffer.YBase, 0)
	h.curAttrData.Fg = h.activeBuffer.SavedCurAttrData.Fg
	h.curAttrData.Bg = h.activeBuffer.SavedCurAttrData.Bg
	h.charsetService.Charset = nil
	if h.activeBuffer.SavedCharset != nil {
		h.charsetService.Charset = h.activeBuffer.SavedCharset
	}
	h.restrictCursor(h.bufferService.Cols - 1)
	return true
}

// SetTitle handles OSC 0/2.
func (h *InputHandler) SetTitle(data string) bool {
	h.windowTitle = data
	if h.OnTitleChange != nil {
		h.OnTitleChange(data)
	}
	return true
}

// SetIconName handles OSC 0/1 (icon name is not exposed).
func (h *InputHandler) SetIconName(data string) bool {
	h.iconName = data
	return true
}

// SetOrReportIndexedColor handles OSC 4.
func (h *InputHandler) SetOrReportIndexedColor(data string) bool {
	var event []ColorEvent
	slots := strings.Split(data, ";")
	for len(slots) > 1 {
		idx := slots[0]
		spec := slots[1]
		slots = slots[2:]
		if index, err := strconv.Atoi(idx); err == nil && index >= 0 && index < 256 {
			if spec == "?" {
				event = append(event, ColorEvent{Type: ColorRequestReport, Index: index})
			} else if color, ok := ParseColor(spec); ok {
				event = append(event, ColorEvent{Type: ColorRequestSet, Index: index, Color: color})
			}
		}
	}
	if len(event) > 0 && h.OnColor != nil {
		h.OnColor(event)
	}
	return true
}

// SetHyperlink handles OSC 8.
func (h *InputHandler) SetHyperlink(data string) bool {
	// arg parsing supports unencoded semi-colons in the URI
	idx := strings.Index(data, ";")
	if idx == -1 {
		// malformed sequence, just return as handled
		return true
	}
	id := strings.TrimSpace(data[:idx])
	uri := data[idx+1:]
	if uri != "" {
		return h.createHyperlink(id, uri)
	}
	if id != "" {
		return false
	}
	return h.finishHyperlink()
}

func (h *InputHandler) createHyperlink(params, uri string) bool {
	// it's legal to open a new hyperlink without finishing the previous
	if h.getCurrentLinkID() != 0 {
		h.finishHyperlink()
	}
	var id string
	for _, p := range strings.Split(params, ":") {
		if strings.HasPrefix(p, "id=") {
			id = p[3:]
			break
		}
	}
	h.curAttrData.Extended = h.curAttrData.Extended.Clone()
	h.curAttrData.Extended.URLID = h.oscLinkService.RegisterLink(id, uri)
	h.curAttrData.UpdateExtended()
	return true
}

func (h *InputHandler) finishHyperlink() bool {
	h.curAttrData.Extended = h.curAttrData.Extended.Clone()
	h.curAttrData.Extended.URLID = 0
	h.curAttrData.UpdateExtended()
	return true
}

// special colors for OSC 10 | 11 | 12
var specialColors = []int{SpecialColorForeground, SpecialColorBackground, SpecialColorCursor}

func (h *InputHandler) setOrReportSpecialColor(data string, offset int) bool {
	slots := strings.Split(data, ";")
	for i := 0; i < len(slots); i, offset = i+1, offset+1 {
		if offset >= len(specialColors) {
			break
		}
		if slots[i] == "?" {
			if h.OnColor != nil {
				h.OnColor([]ColorEvent{{Type: ColorRequestReport, Index: specialColors[offset]}})
			}
		} else if color, ok := ParseColor(slots[i]); ok {
			if h.OnColor != nil {
				h.OnColor([]ColorEvent{{Type: ColorRequestSet, Index: specialColors[offset], Color: color}})
			}
		}
	}
	return true
}

// SetOrReportFgColor handles OSC 10.
func (h *InputHandler) SetOrReportFgColor(data string) bool {
	return h.setOrReportSpecialColor(data, 0)
}

// SetOrReportBgColor handles OSC 11.
func (h *InputHandler) SetOrReportBgColor(data string) bool {
	return h.setOrReportSpecialColor(data, 1)
}

// SetOrReportCursorColor handles OSC 12.
func (h *InputHandler) SetOrReportCursorColor(data string) bool {
	return h.setOrReportSpecialColor(data, 2)
}

// RestoreIndexedColor handles OSC 104.
func (h *InputHandler) RestoreIndexedColor(data string) bool {
	if data == "" {
		if h.OnColor != nil {
			h.OnColor([]ColorEvent{{Type: ColorRequestRestore, Index: -1}})
		}
		return true
	}
	var event []ColorEvent
	for _, slot := range strings.Split(data, ";") {
		if index, err := strconv.Atoi(slot); err == nil && index >= 0 && index < 256 {
			event = append(event, ColorEvent{Type: ColorRequestRestore, Index: index})
		}
	}
	if len(event) > 0 && h.OnColor != nil {
		h.OnColor(event)
	}
	return true
}

// RestoreFgColor handles OSC 110.
func (h *InputHandler) RestoreFgColor(data string) bool {
	if h.OnColor != nil {
		h.OnColor([]ColorEvent{{Type: ColorRequestRestore, Index: SpecialColorForeground}})
	}
	return true
}

// RestoreBgColor handles OSC 111.
func (h *InputHandler) RestoreBgColor(data string) bool {
	if h.OnColor != nil {
		h.OnColor([]ColorEvent{{Type: ColorRequestRestore, Index: SpecialColorBackground}})
	}
	return true
}

// RestoreCursorColor handles OSC 112.
func (h *InputHandler) RestoreCursorColor(data string) bool {
	if h.OnColor != nil {
		h.OnColor([]ColorEvent{{Type: ColorRequestRestore, Index: SpecialColorCursor}})
	}
	return true
}

// NextLine handles NEL.
func (h *InputHandler) NextLine() bool {
	h.activeBuffer.X = 0
	h.Index()
	return true
}

// KeypadApplicationMode handles DECKPAM.
func (h *InputHandler) KeypadApplicationMode() bool {
	h.coreService.DecPrivateModes.ApplicationKeypad = true
	if h.OnRequestSyncScrollBar != nil {
		h.OnRequestSyncScrollBar()
	}
	return true
}

// KeypadNumericMode handles DECKPNM.
func (h *InputHandler) KeypadNumericMode() bool {
	h.coreService.DecPrivateModes.ApplicationKeypad = false
	if h.OnRequestSyncScrollBar != nil {
		h.OnRequestSyncScrollBar()
	}
	return true
}

// SelectDefaultCharset handles ESC % @ and ESC % G.
func (h *InputHandler) SelectDefaultCharset() bool {
	h.charsetService.SetgLevel(0)
	h.charsetService.SetgCharset(0, DefaultCharset)
	return true
}

// SelectCharset designates a charset (ESC ( C etc.).
func (h *InputHandler) SelectCharset(collectAndFlag string) bool {
	if len(collectAndFlag) != 2 {
		h.SelectDefaultCharset()
		return true
	}
	if collectAndFlag[0] == '/' {
		return true // TODO: is this supported?
	}
	h.charsetService.SetgCharset(glevelMap[collectAndFlag[0]], Charsets[collectAndFlag[1]])
	return true
}

// Index handles IND.
func (h *InputHandler) Index() bool {
	h.restrictCursor(h.bufferService.Cols - 1)
	h.activeBuffer.Y++
	if h.activeBuffer.Y == h.activeBuffer.ScrollBottom+1 {
		h.activeBuffer.Y--
		h.bufferService.Scroll(h.eraseAttrData(), false)
	} else if h.activeBuffer.Y >= h.bufferService.Rows {
		h.activeBuffer.Y = h.bufferService.Rows - 1
	}
	h.restrictCursor(h.bufferService.Cols - 1)
	return true
}

// TabSet handles HTS.
func (h *InputHandler) TabSet() bool {
	h.activeBuffer.Tabs[h.activeBuffer.X] = true
	return true
}

// ReverseIndex handles RI.
func (h *InputHandler) ReverseIndex() bool {
	h.restrictCursor(h.bufferService.Cols - 1)
	if h.activeBuffer.Y == h.activeBuffer.ScrollTop {
		// blankLine(true) is xterm/linux behavior
		scrollRegionHeight := h.activeBuffer.ScrollBottom - h.activeBuffer.ScrollTop
		h.activeBuffer.Lines.ShiftElements(h.activeBuffer.YBase+h.activeBuffer.Y, scrollRegionHeight, 1)
		h.activeBuffer.Lines.Set(h.activeBuffer.YBase+h.activeBuffer.Y, h.activeBuffer.GetBlankLine(h.eraseAttrData(), false))
		h.dirtyTracker.markRangeDirty(h.activeBuffer.ScrollTop, h.activeBuffer.ScrollBottom)
	} else {
		h.activeBuffer.Y--
		h.restrictCursor(h.bufferService.Cols - 1)
	}
	return true
}

// FullReset handles RIS.
func (h *InputHandler) FullReset() bool {
	h.parser.Reset()
	if h.OnRequestReset != nil {
		h.OnRequestReset()
	}
	return true
}

// Reset resets the handler attributes (called from the terminal
// reset).
func (h *InputHandler) Reset() {
	h.curAttrData = NewAttributeData()
	h.eraseAttrDataInternal = NewAttributeData()
}

// eraseAttrData implements the back_color_erase feature: erased cells
// keep the current background.
func (h *InputHandler) eraseAttrData() *AttributeData {
	h.eraseAttrDataInternal.Bg &= ^(AttrCMMask | 0xFFFFFF)
	h.eraseAttrDataInternal.Bg |= h.curAttrData.Bg & ^uint32(0xFC000000)
	return h.eraseAttrDataInternal
}

// SetgLevel handles the locking shifts (ESC n/o/|/}/~).
func (h *InputHandler) SetgLevel(level int) bool {
	h.charsetService.SetgLevel(level)
	return true
}

// ScreenAlignmentPattern handles DECALN.
func (h *InputHandler) ScreenAlignmentPattern() bool {
	// prepare cell data
	cell := &CellData{AttributeData: AttributeData{Extended: NewExtendedAttrs()}}
	cell.Content = 1<<ContentWidthShift | uint32('E')
	cell.Fg = h.curAttrData.Fg
	cell.Bg = h.curAttrData.Bg

	h.setCursor(0, 0)
	for yOffset := 0; yOffset < h.bufferService.Rows; yOffset++ {
		row := h.activeBuffer.YBase + h.activeBuffer.Y + yOffset
		if row >= h.activeBuffer.Lines.Length() {
			continue
		}
		line := h.activeBuffer.Lines.Get(row)
		line.Fill(cell, false)
		line.IsWrapped = false
	}
	h.dirtyTracker.markAllDirty()
	h.setCursor(0, 0)
	return true
}

// RequestStatusString handles DECRQSS.
func (h *InputHandler) RequestStatusString(data string, params *Params) bool {
	f := func(s string) bool {
		h.coreService.TriggerDataEvent(c0ESC+s+c0ESC+"\\", false)
		return true
	}

	b := h.bufferService.Buffer()
	styles := map[string]int{"block": 2, "underline": 4, "bar": 6}

	switch data {
	case "\"q":
		v := 0
		if h.curAttrData.IsProtected() {
			v = 1
		}
		return f(fmt.Sprintf("P1$r%d\"q", v))
	case "\"p":
		return f("P1$r61;1\"p")
	case "r":
		return f(fmt.Sprintf("P1$r%d;%dr", b.ScrollTop+1, b.ScrollBottom+1))
	case "m":
		// FIXME (in xterm.js too): report real SGR settings instead of 0m
		return f("P1$r0m")
	case " q":
		style := styles[h.options.CursorStyle]
		if h.options.CursorBlink {
			style--
		}
		return f(fmt.Sprintf("P1$r%d q", style))
	}
	return f("P0$r")
}

// MarkRangeDirty exposes dirty marking for the terminal (used on
// scroll events).
func (h *InputHandler) MarkRangeDirty(y1, y2 int) {
	h.dirtyTracker.markRangeDirty(y1, y2)
}

// WindowTitle returns the current window title.
func (h *InputHandler) WindowTitle() string { return h.windowTitle }

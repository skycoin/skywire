package vt

// Port of src/common/CoreTerminal.ts — the headless terminal that owns
// all services and the input handler. The DI container, log service
// and the async WriteBuffer time-slicing are dropped (writes are
// synchronous; the browser layer re-adds chunked scheduling where
// needed). Windows conpty wrapping heuristics are not ported.

// Terminal is a headless VT terminal.
type Terminal struct {
	Options *Options

	bufferService  *BufferService
	coreService    *CoreService
	charsetService *CharsetService
	mouseService   *CoreMouseService
	oscLinkService *OscLinkService
	inputHandler   *InputHandler

	// OnData receives data the terminal sends to the pty (user input,
	// query responses).
	OnData func(data string)
	// OnBinary receives binary data for the pty (legacy mouse
	// encoding).
	OnBinary func(data string)
	// OnLineFeed fires on line feeds.
	OnLineFeed func()
	// OnResize fires after the dimensions changed.
	OnResize func(cols, rows int)
	// OnScroll fires with the new viewport offset.
	OnScroll func(ydisp int)
	// OnTitleChange fires when the window title changes.
	OnTitleChange func(title string)
	// OnRefreshRows asks the renderer to redraw viewport rows
	// start..end (inclusive); -1,-1 means everything.
	OnRefreshRows func(start, end int)
	// OnBell fires on BEL.
	OnBell func()
	// OnColor receives OSC color set/report/restore requests.
	OnColor func(events []ColorEvent)
	// OnCursorMove fires when the cursor moved during a write.
	OnCursorMove func()
	// OnA11yChar/OnA11yTab fire in screen reader mode.
	OnA11yChar func(char string)
	OnA11yTab  func(spaces int)
	// OnWindowsOptionsReport asks the browser layer to report pixel
	// sizes (CSI t 14/16).
	OnWindowsOptionsReport func(reportType int)
	// OnSyncScrollBar asks the viewport to sync (keypad/alt-buffer
	// changes).
	OnSyncScrollBar func()
	// OnSendFocus asks the browser layer to send a focus report.
	OnSendFocus func()
	// OnProtocolChange fires with the mouse event mask needed by the
	// active mouse protocol.
	OnProtocolChange func(events int)
}

// NewTerminal creates a headless terminal. Pass nil for defaults.
func NewTerminal(options *Options) *Terminal {
	if options == nil {
		options = NewOptions()
	}
	t := &Terminal{Options: options}

	t.bufferService = NewBufferService(options)
	t.coreService = NewCoreService(t.bufferService, options)
	t.charsetService = &CharsetService{}
	t.mouseService = NewCoreMouseService(t.bufferService, t.coreService, options)
	t.oscLinkService = NewOscLinkService()
	t.inputHandler = NewInputHandler(t.bufferService, t.charsetService, t.coreService, t.mouseService, t.oscLinkService, options)

	// forward events
	t.coreService.OnData = func(data string) {
		if t.OnData != nil {
			t.OnData(data)
		}
	}
	t.coreService.OnBinary = func(data string) {
		if t.OnBinary != nil {
			t.OnBinary(data)
		}
	}
	t.coreService.OnRequestScrollToBottom = func() { t.ScrollToBottom() }
	t.bufferService.OnResize = func(cols, rows int) {
		if t.OnResize != nil {
			t.OnResize(cols, rows)
		}
	}
	t.bufferService.OnScroll = func(ydisp int) {
		if t.OnScroll != nil {
			t.OnScroll(ydisp)
		}
		buffer := t.bufferService.Buffer()
		t.inputHandler.MarkRangeDirty(buffer.ScrollTop, buffer.ScrollBottom)
	}
	t.mouseService.OnProtocolChange = func(events int) {
		if t.OnProtocolChange != nil {
			t.OnProtocolChange(events)
		}
	}
	t.inputHandler.OnRequestReset = func() { t.Reset() }
	t.inputHandler.OnLineFeed = func() {
		if t.OnLineFeed != nil {
			t.OnLineFeed()
		}
	}
	t.inputHandler.OnTitleChange = func(title string) {
		if t.OnTitleChange != nil {
			t.OnTitleChange(title)
		}
	}
	t.inputHandler.OnRequestRefreshRows = func(start, end int) {
		if t.OnRefreshRows != nil {
			t.OnRefreshRows(start, end)
		}
	}
	t.inputHandler.OnRequestBell = func() {
		if t.OnBell != nil {
			t.OnBell()
		}
	}
	t.inputHandler.OnColor = func(events []ColorEvent) {
		if t.OnColor != nil {
			t.OnColor(events)
		}
	}
	t.inputHandler.OnCursorMove = func() {
		if t.OnCursorMove != nil {
			t.OnCursorMove()
		}
	}
	t.inputHandler.OnA11yChar = func(char string) {
		if t.OnA11yChar != nil {
			t.OnA11yChar(char)
		}
	}
	t.inputHandler.OnA11yTab = func(spaces int) {
		if t.OnA11yTab != nil {
			t.OnA11yTab(spaces)
		}
	}
	t.inputHandler.OnRequestWindowsOptionsReport = func(reportType int) {
		if t.OnWindowsOptionsReport != nil {
			t.OnWindowsOptionsReport(reportType)
		}
	}
	t.inputHandler.OnRequestSyncScrollBar = func() {
		if t.OnSyncScrollBar != nil {
			t.OnSyncScrollBar()
		}
	}
	t.inputHandler.OnRequestSendFocus = func() {
		if t.OnSendFocus != nil {
			t.OnSendFocus()
		}
	}
	t.inputHandler.OnScroll = func(ydisp int) {
		if t.OnScroll != nil {
			t.OnScroll(ydisp)
		}
	}

	return t
}

// Cols returns the current column count.
func (t *Terminal) Cols() int { return t.bufferService.Cols }

// Rows returns the current row count.
func (t *Terminal) Rows() int { return t.bufferService.Rows }

// Buffer returns the active buffer.
func (t *Terminal) Buffer() *Buffer { return t.bufferService.Buffer() }

// Buffers returns the buffer set (normal and alt).
func (t *Terminal) Buffers() *BufferSet { return t.bufferService.Buffers }

// CoreService exposes modes and the data event trigger.
func (t *Terminal) CoreService() *CoreService { return t.coreService }

// MouseService exposes the mouse tracking service.
func (t *Terminal) MouseService() *CoreMouseService { return t.mouseService }

// OscLinkService exposes registered OSC 8 hyperlinks.
func (t *Terminal) OscLinkService() *OscLinkService { return t.oscLinkService }

// InputHandler exposes the input handler (attr data, custom handler
// registration).
func (t *Terminal) InputHandler() *InputHandler { return t.inputHandler }

// Write parses a chunk of UTF-8 encoded pty output.
func (t *Terminal) Write(data []byte) {
	t.inputHandler.Parse(data)
}

// WriteString parses a chunk given as string.
func (t *Terminal) WriteString(data string) {
	t.inputHandler.ParseString(data)
}

// Input sends data to the pty (keyboard input path).
func (t *Terminal) Input(data string, wasUserInput bool) {
	t.coreService.TriggerDataEvent(data, wasUserInput)
}

// Resize changes the terminal dimensions.
func (t *Terminal) Resize(cols, rows int) {
	cols = maxInt(cols, MinimumCols)
	rows = maxInt(rows, MinimumRows)
	t.bufferService.Resize(cols, rows)
}

// Scroll scrolls down one row, creating a blank line (used by the
// input handler via the buffer service).
func (t *Terminal) Scroll(eraseAttr *AttributeData, isWrapped bool) {
	t.bufferService.Scroll(eraseAttr, isWrapped)
}

// ScrollLines scrolls the display (negative = up).
func (t *Terminal) ScrollLines(disp int) {
	t.bufferService.ScrollLines(disp, false)
}

// ScrollPages scrolls by pages.
func (t *Terminal) ScrollPages(pageCount int) {
	t.ScrollLines(pageCount * (t.Rows() - 1))
}

// ScrollToTop scrolls to the top of the scrollback.
func (t *Terminal) ScrollToTop() {
	t.ScrollLines(-t.bufferService.Buffer().YDisp)
}

// ScrollToBottom scrolls back to the prompt.
func (t *Terminal) ScrollToBottom() {
	t.ScrollLines(t.bufferService.Buffer().YBase - t.bufferService.Buffer().YDisp)
}

// ScrollToLine scrolls to an absolute line in the buffer.
func (t *Terminal) ScrollToLine(line int) {
	scrollAmount := line - t.bufferService.Buffer().YDisp
	if scrollAmount != 0 {
		t.ScrollLines(scrollAmount)
	}
}

// Reset restores the terminal to its initial state.
func (t *Terminal) Reset() {
	t.inputHandler.Reset()
	t.bufferService.Reset()
	t.charsetService.Reset()
	t.coreService.Reset()
	t.mouseService.Reset()
}

package vt

// Port of src/common/services/CoreService.ts, CharsetService.ts,
// BufferService.ts and src/common/buffer/BufferSet.ts. Event emitters
// become plain callback fields; the DI service registry is dropped —
// services hold direct references to each other.

// Modes holds the ANSI-standardized modes (IModes).
type Modes struct {
	InsertMode bool
}

// DecPrivateModes holds the DEC private modes (IDecPrivateModes).
type DecPrivateModes struct {
	ApplicationCursorKeys bool
	ApplicationKeypad     bool
	BracketedPasteMode    bool
	// CursorBlink/CursorStyle: nil = not overridden by DECSCUSR.
	CursorBlink        *bool
	CursorStyle        *string
	Origin             bool
	ReverseWraparound  bool
	SendFocus          bool
	SynchronizedOutput bool
	Wraparound         bool // defaults: xterm - true, vt100 - false
}

func defaultDecPrivateModes() DecPrivateModes {
	return DecPrivateModes{Wraparound: true}
}

// CoreService is the pty-facing output side of the terminal: data the
// terminal wants to send back (keystrokes, query responses) goes
// through TriggerDataEvent.
type CoreService struct {
	IsCursorInitialized bool
	IsCursorHidden      bool
	Modes               Modes
	DecPrivateModes     DecPrivateModes

	// OnData receives data headed for the pty (user input and
	// terminal query responses).
	OnData func(data string)
	// OnUserInput fires on user-originated input (e.g. to clear the
	// selection).
	OnUserInput func()
	// OnBinary receives binary data headed for the pty.
	OnBinary func(data string)
	// OnRequestScrollToBottom fires when user input should snap the
	// viewport back to the bottom.
	OnRequestScrollToBottom func()

	bufferService *BufferService
	options       *Options
}

// NewCoreService creates a CoreService bound to a buffer service and
// options.
func NewCoreService(bufferService *BufferService, options *Options) *CoreService {
	return &CoreService{
		Modes:           Modes{},
		DecPrivateModes: defaultDecPrivateModes(),
		bufferService:   bufferService,
		options:         options,
	}
}

// Reset restores default modes.
func (c *CoreService) Reset() {
	c.Modes = Modes{}
	c.DecPrivateModes = defaultDecPrivateModes()
}

// TriggerDataEvent sends data to the pty unless stdin is disabled.
func (c *CoreService) TriggerDataEvent(data string, wasUserInput bool) {
	// Prevents all events to pty process if stdin is disabled
	if c.options.DisableStdin {
		return
	}

	// Input is being sent to the terminal, the terminal should focus the prompt.
	buffer := c.bufferService.Buffer()
	if wasUserInput && c.options.ScrollOnUserInput && buffer.YBase != buffer.YDisp {
		if c.OnRequestScrollToBottom != nil {
			c.OnRequestScrollToBottom()
		}
	}

	if wasUserInput && c.OnUserInput != nil {
		c.OnUserInput()
	}

	if c.OnData != nil {
		c.OnData(data)
	}
}

// TriggerBinaryEvent sends binary data to the pty unless stdin is
// disabled.
func (c *CoreService) TriggerBinaryEvent(data string) {
	if c.options.DisableStdin {
		return
	}
	if c.OnBinary != nil {
		c.OnBinary(data)
	}
}

// CharsetService tracks the active charset and the G0-G3 slots.
type CharsetService struct {
	Charset Charset
	Glevel  int

	charsets [4]Charset
	set      [4]bool
}

// Reset clears all designated charsets.
func (s *CharsetService) Reset() {
	s.Charset = nil
	s.charsets = [4]Charset{}
	s.set = [4]bool{}
	s.Glevel = 0
}

// SetgLevel selects which G slot is active (shift in/out, LS2, LS3).
func (s *CharsetService) SetgLevel(g int) {
	s.Glevel = g
	s.Charset = s.charsets[g]
}

// SetgCharset designates a charset into a G slot.
func (s *CharsetService) SetgCharset(g int, charset Charset) {
	s.charsets[g] = charset
	s.set[g] = true
	if s.Glevel == g {
		s.Charset = charset
	}
}

// Minimum dimensions (less than 2 cols can mess with wide chars).
const (
	MinimumCols = 2
	MinimumRows = 1
)

// BufferSet represents the normal and alt buffers used by xterm
// terminals.
type BufferSet struct {
	normal *Buffer
	alt    *Buffer
	active *Buffer

	options *Options
	cols    int
	rows    int

	// OnBufferActivate fires when the active buffer switches.
	OnBufferActivate func(active, inactive *Buffer)
}

// NewBufferSet creates the normal/alt buffer pair.
func NewBufferSet(options *Options, cols, rows int) *BufferSet {
	s := &BufferSet{options: options, cols: cols, rows: rows}
	s.Reset()
	return s
}

// Reset recreates both buffers and activates the normal one.
func (s *BufferSet) Reset() {
	s.normal = NewBuffer(true, s.options, s.cols, s.rows)
	s.normal.FillViewportRows(nil)

	// The alt buffer should never have scrollback.
	s.alt = NewBuffer(false, s.options, s.cols, s.rows)
	s.active = s.normal
	if s.OnBufferActivate != nil {
		s.OnBufferActivate(s.normal, s.alt)
	}

	s.SetupTabStops(-1)
}

// Alt returns the alt buffer.
func (s *BufferSet) Alt() *Buffer { return s.alt }

// Active returns the currently active buffer.
func (s *BufferSet) Active() *Buffer { return s.active }

// Normal returns the normal buffer.
func (s *BufferSet) Normal() *Buffer { return s.normal }

// ActivateNormalBuffer switches to the normal buffer, clearing the alt
// buffer.
func (s *BufferSet) ActivateNormalBuffer() {
	if s.active == s.normal {
		return
	}
	s.normal.X = s.alt.X
	s.normal.Y = s.alt.Y
	// The alt buffer should always be cleared when we switch to the
	// normal buffer; this frees up memory since the alt buffer should
	// always be new when activated.
	s.alt.ClearAllMarkers()
	s.alt.Clear()
	s.active = s.normal
	if s.OnBufferActivate != nil {
		s.OnBufferActivate(s.normal, s.alt)
	}
}

// ActivateAltBuffer switches to the alt buffer, filling it with blank
// rows using fillAttr.
func (s *BufferSet) ActivateAltBuffer(fillAttr *AttributeData) {
	if s.active == s.alt {
		return
	}
	// Since the alt buffer is always cleared when the normal buffer is
	// activated, we want to fill it when switching to it.
	s.alt.FillViewportRows(fillAttr)
	s.alt.X = s.normal.X
	s.alt.Y = s.normal.Y
	s.active = s.alt
	if s.OnBufferActivate != nil {
		s.OnBufferActivate(s.alt, s.normal)
	}
}

// Resize resizes both buffers.
func (s *BufferSet) Resize(newCols, newRows int) {
	s.cols = newCols
	s.rows = newRows
	s.normal.Resize(newCols, newRows)
	s.alt.Resize(newCols, newRows)
	s.SetupTabStops(newCols)
}

// SetupTabStops sets up tab stops on both buffers from index i
// (i < 0 = full re-init).
func (s *BufferSet) SetupTabStops(i int) {
	s.normal.SetupTabStops(i)
	s.alt.SetupTabStops(i)
}

// BufferService owns the buffer set and the terminal dimensions, and
// implements scrolling.
type BufferService struct {
	Cols    int
	Rows    int
	Buffers *BufferSet
	// IsUserScrolling locks the scroll position while the user is
	// scrolled up.
	IsUserScrolling bool

	// OnResize fires after a resize with the new dimensions.
	OnResize func(cols, rows int)
	// OnScroll fires with the new YDisp whenever the viewport moves.
	OnScroll func(ydisp int)

	cachedBlankLine *BufferLine
}

// NewBufferService creates the buffer service with dimensions from the
// options.
func NewBufferService(options *Options) *BufferService {
	s := &BufferService{
		Cols: max(options.Cols, MinimumCols),
		Rows: max(options.Rows, MinimumRows),
	}
	s.Buffers = NewBufferSet(options, s.Cols, s.Rows)
	s.Buffers.OnBufferActivate = func(active, _ *Buffer) {
		if s.OnScroll != nil {
			s.OnScroll(active.YDisp)
		}
	}
	return s
}

// Buffer returns the active buffer.
func (s *BufferService) Buffer() *Buffer { return s.Buffers.Active() }

// Resize changes the terminal dimensions.
func (s *BufferService) Resize(cols, rows int) {
	s.Cols = cols
	s.Rows = rows
	s.Buffers.Resize(cols, rows)
	if s.OnResize != nil {
		s.OnResize(cols, rows)
	}
}

// Reset resets the buffers.
func (s *BufferService) Reset() {
	s.Buffers.Reset()
	s.IsUserScrolling = false
}

// Scroll scrolls the terminal down 1 row, creating a blank line at the
// bottom. eraseAttr is the attribute data for the blank line.
func (s *BufferService) Scroll(eraseAttr *AttributeData, isWrapped bool) {
	buffer := s.Buffer()

	newLine := s.cachedBlankLine
	if newLine == nil || newLine.Length != s.Cols || newLine.GetFg(0) != eraseAttr.Fg || newLine.GetBg(0) != eraseAttr.Bg {
		newLine = buffer.GetBlankLine(eraseAttr, isWrapped)
		s.cachedBlankLine = newLine
	}
	newLine.IsWrapped = isWrapped

	topRow := buffer.YBase + buffer.ScrollTop
	bottomRow := buffer.YBase + buffer.ScrollBottom

	if buffer.ScrollTop == 0 {
		// Determine whether the buffer is going to be trimmed after insertion.
		willBufferBeTrimmed := buffer.Lines.IsFull()

		// Insert the line using the fastest method
		if bottomRow == buffer.Lines.Length()-1 {
			if willBufferBeTrimmed {
				buffer.Lines.Recycle().CopyFrom(newLine)
			} else {
				buffer.Lines.Push(newLine.Clone())
			}
		} else {
			buffer.Lines.Splice(bottomRow+1, 0, newLine.Clone())
		}

		// Only adjust ybase and ydisp when the buffer is not trimmed
		if !willBufferBeTrimmed {
			buffer.YBase++
			// Only scroll the ydisp with ybase if the user has not scrolled up
			if !s.IsUserScrolling {
				buffer.YDisp++
			}
		} else {
			// When the buffer is full and the user has scrolled up, keep the
			// text stable unless ydisp is right at the top
			if s.IsUserScrolling {
				buffer.YDisp = max(buffer.YDisp-1, 0)
			}
		}
	} else {
		// scrollTop is non-zero which means no line will be going to the
		// scrollback, instead we can just shift them in-place.
		scrollRegionHeight := bottomRow - topRow + 1
		buffer.Lines.ShiftElements(topRow+1, scrollRegionHeight-1, -1)
		buffer.Lines.Set(bottomRow, newLine.Clone())
	}

	// Move the viewport to the bottom of the buffer unless the user is
	// scrolling.
	if !s.IsUserScrolling {
		buffer.YDisp = buffer.YBase
	}

	if s.OnScroll != nil {
		s.OnScroll(buffer.YDisp)
	}
}

// ScrollLines scrolls the display by disp lines (negative = up).
func (s *BufferService) ScrollLines(disp int, suppressScrollEvent bool) {
	buffer := s.Buffer()
	if disp < 0 {
		if buffer.YDisp == 0 {
			return
		}
		s.IsUserScrolling = true
	} else if disp+buffer.YDisp >= buffer.YBase {
		s.IsUserScrolling = false
	}

	oldYdisp := buffer.YDisp
	buffer.YDisp = max(min(buffer.YDisp+disp, buffer.YBase), 0)

	// No change occurred, don't trigger scroll/refresh
	if oldYdisp == buffer.YDisp {
		return
	}

	if !suppressScrollEvent && s.OnScroll != nil {
		s.OnScroll(buffer.YDisp)
	}
}

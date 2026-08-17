package vt

// Options is the subset of the xterm.js options relevant to the core
// (port of the OptionsService raw options; browser-only options live in
// the wasm layer).
type Options struct {
	// Cols/Rows of the terminal. Defaults: 80x24.
	Cols int
	Rows int
	// Scrollback line count. Default: 1000.
	Scrollback int
	// TabStopWidth. Default: 8.
	TabStopWidth int
	// CursorBlink toggles cursor blinking. Default: false.
	CursorBlink bool
	// ConvertEol treats LF as CRLF. Default: false.
	ConvertEol bool
	// DisableStdin ignores keyboard input. Default: false.
	DisableStdin bool
	// ScreenReaderMode. Default: false.
	ScreenReaderMode bool
	// WindowsMode disables reflow (conpty quirk mode). Default: false.
	WindowsMode bool
	// ReflowCursorLine reflows the cursor line on resize. Default: false.
	ReflowCursorLine bool
	// TermName reported by DA sequences. Default: "xterm".
	TermName string
	// CursorStyle: "block", "underline" or "bar". Default: "block".
	CursorStyle string
	// ScrollOnUserInput snaps the viewport to the bottom on input.
	// Default: true.
	ScrollOnUserInput bool
	// ScrollOnEraseInDisplay scrolls content into scrollback on ED 2
	// instead of clearing it. Default: false.
	ScrollOnEraseInDisplay bool
	// ScrollSensitivity scaling for wheel scroll. Default: 1.
	ScrollSensitivity float64
	// FastScrollSensitivity applied while the modifier is held. Default: 5.
	FastScrollSensitivity float64
	// WindowOptions enables individual CSI t window commands (all
	// default false, security).
	WindowOptions WindowOptions
	// FontFamily/FontSize used by the renderer.
	FontFamily string
	FontSize   float64
	// LineHeight multiplier. Default: 1.
	LineHeight float64
	// LetterSpacing in px. Default: 0.
	LetterSpacing float64
	// Theme colors (CSS color strings; empty = defaults).
	Theme Theme
}

// Theme holds the terminal colors (port of ITheme).
type Theme struct {
	Foreground          string
	Background          string
	Cursor              string
	CursorAccent        string
	SelectionBackground string
	Black               string
	Red                 string
	Green               string
	Yellow              string
	Blue                string
	Magenta             string
	Cyan                string
	White               string
	BrightBlack         string
	BrightRed           string
	BrightGreen         string
	BrightYellow        string
	BrightBlue          string
	BrightMagenta       string
	BrightCyan          string
	BrightWhite         string
}

// WindowOptions gates the CSI t window manipulation commands (port of
// IWindowOptions; everything defaults to off like in xterm.js).
type WindowOptions struct {
	RestoreWin          bool
	MinimizeWin         bool
	SetWinPosition      bool
	SetWinSizePixels    bool
	RaiseWin            bool
	LowerWin            bool
	RefreshWin          bool
	SetWinSizeChars     bool
	MaximizeWin         bool
	FullscreenWin       bool
	GetWinState         bool
	GetWinPosition      bool
	GetWinSizePixels    bool
	GetScreenSizePixels bool
	GetCellSizePixels   bool
	GetWinSizeChars     bool
	GetScreenSizeChars  bool
	GetIconTitle        bool
	GetWinTitle         bool
	PushTitle           bool
	PopTitle            bool
	SetWinLines         bool
}

// NewOptions returns options with xterm.js defaults.
func NewOptions() *Options {
	return &Options{
		Cols:                  80,
		Rows:                  24,
		Scrollback:            1000,
		TabStopWidth:          8,
		FontFamily:            "monospace",
		FontSize:              15,
		LineHeight:            1.0,
		LetterSpacing:         0,
		TermName:              "xterm",
		CursorStyle:           "block",
		ScrollOnUserInput:     true,
		ScrollSensitivity:     1,
		FastScrollSensitivity: 5,
	}
}

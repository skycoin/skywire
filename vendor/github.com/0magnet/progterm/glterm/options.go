package glterm

import "os"

// Options configure Run.
type Options struct {
	// Shell and Args are the program the window runs.
	Shell string
	Args  []string
	// Run, when set, runs in this process in place of Shell, on a pty of its
	// own (StartFunc), in the first tab and each tab opened after it.
	Run func(tty *os.File, sig <-chan os.Signal) int
	// Font is the font, "Family Size" as GTK names one ("DejaVu Sans Mono 10").
	// Empty means the desktop terminal's (DesktopFont), and Go Mono where
	// there is none or it cannot be loaded.
	Font string
	// FontPt, when set, is the font size in points in place of Font's.
	FontPt float64
	// Cols and Rows are the size a window opens at, in cells. Zero means 100
	// by 30, made smaller where the screen is.
	Cols, Rows int
	// Title is the window's title. Empty means progterm.
	Title string
	// Tabs are more programs a window with tabs opens beside the first.
	Tabs []Tab
}

// Tab is a program a tab runs: Run in this process, or else Shell, where an
// empty Shell is the Options' own.
type Tab struct {
	Title string
	Shell string
	Args  []string
	Run   func(tty *os.File, sig <-chan os.Signal) int
}

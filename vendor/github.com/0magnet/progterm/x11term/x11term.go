//go:build unix && !android && !ios

// Package x11term is glterm's terminal in an X11 window, spoken to over the X
// protocol in Go: no cgo and no X libraries, so any build can open one, on
// an X server or XWayland, local or remote.
package x11term

import (
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"

	"github.com/0magnet/progterm/glterm"
)

// Available reports whether there is an X display to open a window on.
func Available() bool { return os.Getenv("DISPLAY") != "" }

// Run opens a window on $DISPLAY running opt's programs in tabs, and returns
// when the last one exits or the window is closed.
func Run(opt glterm.Options) error {
	w, err := open(opt, "")
	if err != nil {
		return err
	}
	return w.run()
}

// window is the X window and the terminals in it. Everything but invalidate
// and do runs on the goroutine in run.
type window struct {
	opt   glterm.Options
	conn  *xgb.Conn
	setup *xproto.SetupInfo
	scr   *xproto.ScreenInfo
	win   xproto.Window
	gc    xproto.Gcontext
	px    pixfmt
	at    atoms
	keys  *keymap
	grid  *glterm.Grid
	ts    glterm.Tabs
	clip  *clipboard

	size    image.Point // the window, in pixels
	stripH  int
	bar     region // the tab strip
	term    region // the current tab's terminal
	buf     []byte
	held    uint8 // buttons 1 to 3 held, as bits 1 to 3
	onStrip bool  // the buttons held were pressed on the strip
	last    xproto.Timestamp
	opened  bool
	exposed bool // the first Expose came, or a moment passed without one
	quit    bool
	later   []func()
	xerrs   int

	events chan xgb.Event
	redraw chan struct{}
	calls  chan func()
}

// open connects to display ($DISPLAY when empty) and maps a window sized
// for opt in cells of its font at the server's Xft.dpi.
func open(opt glterm.Options, display string) (*window, error) {
	xgb.Logger.SetOutput(io.Discard) // it reports, on stderr, an X authority file that is not there
	conn, err := xgb.NewConnDisplay(display)
	if err != nil {
		return nil, fmt.Errorf("x11term: %w", err)
	}
	w := &window{
		opt: opt, conn: conn, setup: xproto.Setup(conn),
		events: make(chan xgb.Event, 64), redraw: make(chan struct{}, 1), calls: make(chan func(), 8),
	}
	w.scr = w.setup.DefaultScreen(conn)
	w.clip = &clipboard{w: w, owned: map[xproto.Atom]string{}}
	if err := w.init(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("x11term: %w", err)
	}
	return w, nil
}

func (w *window) init() error {
	var err error
	if w.px, err = pixelFormat(w.setup, w.scr); err != nil {
		return err
	}
	if w.at, err = internAtoms(w.conn); err != nil {
		return err
	}
	if err := w.loadKeymap(); err != nil {
		return err
	}
	f, ferr := glterm.LoadFont(w.opt.Font)
	if ferr != nil {
		fmt.Fprintf(os.Stderr, "progterm: font: %v, using %s\n", ferr, f.Family)
	}
	if w.opt.FontPt > 0 {
		f.Pt = w.opt.FontPt
	}
	scale := 1.0
	if dpi := parseDPI(w.resources()); dpi > 0 {
		scale = dpi / 96
	}
	if w.grid, err = glterm.NewGridFont(f, f.Pt*glterm.DpPerPt()*scale); err != nil {
		return err
	}
	cols, rows := w.opt.Cols, w.opt.Rows
	if cols <= 0 || rows <= 0 {
		cols, rows = 100, 30
	}
	w.stripH = glterm.StripHeight(w.grid)
	w.size = glterm.WindowSize(w.grid, cols, rows, image.Pt(int(w.scr.WidthInPixels), int(w.scr.HeightInPixels)))
	return w.createWindow()
}

// resources is the server's RESOURCE_MANAGER string, as xrdb sets it.
func (w *window) resources() string {
	r, err := xproto.GetProperty(w.conn, false, w.scr.Root, xproto.AtomResourceManager, xproto.AtomString, 0, 1<<16).Reply()
	if err != nil || r == nil {
		return ""
	}
	return string(r.Value)
}

// parseDPI is Xft.dpi in an X resources string, or 0.
func parseDPI(res string) float64 {
	for _, line := range strings.Split(res, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Xft.dpi:"); ok {
			if dpi, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && dpi > 0 {
				return dpi
			}
		}
	}
	return 0
}

func (w *window) loadKeymap() error {
	first, last := w.setup.MinKeycode, w.setup.MaxKeycode
	km, err := xproto.GetKeyboardMapping(w.conn, first, byte(int(last)-int(first)+1)).Reply() //nolint:gosec // keycodes are 8 to 255
	if err != nil {
		return err
	}
	mm, err := xproto.GetModifierMapping(w.conn).Reply()
	if err != nil {
		return err
	}
	syms := make([]uint32, len(km.Keysyms))
	for i, s := range km.Keysyms {
		syms[i] = uint32(s)
	}
	codes := make([]byte, len(mm.Keycodes))
	for i, c := range mm.Keycodes {
		codes[i] = byte(c)
	}
	w.keys = newKeymap(byte(first), int(km.KeysymsPerKeycode), syms, int(mm.KeycodesPerModifier), codes)
	return nil
}

// The X cursor font's text cursor (X11/cursorfont.h, XC_xterm).
const xcXterm = 152

func (w *window) createWindow() error {
	c := w.conn
	id, err := xproto.NewWindowId(c)
	if err != nil {
		return err
	}
	w.win = id
	var cursor xproto.Cursor
	if fid, err := xproto.NewFontId(c); err == nil {
		if xproto.OpenFontChecked(c, fid, 6, "cursor").Check() == nil {
			if cid, err := xproto.NewCursorId(c); err == nil {
				xproto.CreateGlyphCursor(c, cid, fid, fid, xcXterm, xcXterm+1, 0, 0, 0, 0xffff, 0xffff, 0xffff)
				cursor = cid
			}
			xproto.CloseFont(c, fid)
		}
	}
	mask := uint32(xproto.EventMaskExposure | xproto.EventMaskKeyPress | xproto.EventMaskButtonPress |
		xproto.EventMaskButtonRelease | xproto.EventMaskPointerMotion | xproto.EventMaskStructureNotify |
		xproto.EventMaskPropertyChange | xproto.EventMaskFocusChange)
	vmask := uint32(xproto.CwBackPixel | xproto.CwBitGravity | xproto.CwEventMask)
	vals := []uint32{w.scr.BlackPixel, xproto.GravityNorthWest, mask}
	if cursor != 0 {
		vmask |= xproto.CwCursor
		vals = append(vals, uint32(cursor))
	}
	if err := xproto.CreateWindowChecked(c, w.scr.RootDepth, w.win, w.scr.Root, 0, 0,
		uint16(w.size.X), uint16(w.size.Y), 0, xproto.WindowClassInputOutput, w.scr.RootVisual, vmask, vals).Check(); err != nil { //nolint:gosec // a window size
		return err
	}
	gc, err := xproto.NewGcontextId(c)
	if err != nil {
		return err
	}
	w.gc = gc
	xproto.CreateGC(c, gc, xproto.Drawable(w.win), 0, nil)

	title := w.opt.Title
	if title == "" {
		title = "progterm"
	}
	w.setProp(xproto.AtomWmName, xproto.AtomString, 8, []byte(latin1(title)))
	w.setProp(w.at.netWMName, w.at.utf8, 8, []byte(title))
	class := strings.ToLower(title)
	w.setProp(xproto.AtomWmClass, xproto.AtomString, 8, []byte(class+"\x00"+strings.ToUpper(class[:1])+class[1:]+"\x00"))
	w.setProp(w.at.wmProtocols, xproto.AtomAtom, 32, u32(uint32(w.at.wmDelete)))
	w.setProp(w.at.netWMPid, xproto.AtomCardinal, 32, u32(uint32(os.Getpid()))) //nolint:gosec // a pid
	g := w.grid
	// WM_SIZE_HINTS: PMinSize, PResizeInc and PBaseSize (ICCCM 4.1.2.3).
	hints := make([]uint32, 18)
	hints[0] = 1<<4 | 1<<6 | 1<<8
	hints[5], hints[6] = uint32(20*g.CellW), uint32(w.stripH+5*g.CellH) //nolint:gosec // cell sizes
	hints[9], hints[10] = uint32(g.CellW), uint32(g.CellH)              //nolint:gosec // as above
	hints[15], hints[16] = 0, uint32(w.stripH)                          //nolint:gosec // as above
	w.setProp(xproto.AtomWmNormalHints, xproto.AtomWmSizeHints, 32, u32(hints...))
	return xproto.MapWindowChecked(c, w.win).Check()
}

func (w *window) setProp(prop, typ xproto.Atom, format byte, data []byte) {
	xproto.ChangeProperty(w.conn, xproto.PropModeReplace, w.win, prop, typ, format, uint32(len(data)*8/int(format)), data) //nolint:gosec // a property's length
}

// u32 is vs in the byte order of this end of the connection, which the
// server takes them in.
func u32(vs ...uint32) []byte {
	b := make([]byte, 4*len(vs))
	for i, v := range vs {
		xgb.Put32(b[4*i:], v)
	}
	return b
}

// invalidate asks for a redraw. It may be called from any goroutine.
func (w *window) invalidate() {
	select {
	case w.redraw <- struct{}{}:
	default:
	}
}

// do runs f on the window's goroutine.
func (w *window) do(f func()) { w.calls <- f }

// frameGap is the least time between two frames.
const frameGap = 8 * time.Millisecond

func (w *window) run() error {
	defer w.conn.Close()
	defer w.ts.CloseAll()
	go func() {
		for {
			ev, xerr := w.conn.WaitForEvent()
			if ev == nil && xerr == nil {
				close(w.events)
				return
			}
			if xerr != nil {
				w.do(func() { w.xerror(xerr) })
				continue
			}
			w.events <- ev
		}
	}()

	// The tabs open at the first Expose, once a window manager has had its
	// say about the size, or after a moment without one.
	openAt := time.NewTimer(time.Second)
	defer openAt.Stop()
	frame := time.NewTimer(time.Hour)
	defer frame.Stop()
	dirty, armed := false, false
	var lastFrame time.Time
	for !w.quit {
		select {
		case ev, ok := <-w.events:
			if !ok {
				return errors.New("x11term: the X server closed the connection")
			}
			dirty = w.handle(ev) || dirty
		case <-w.redraw:
			dirty = true
		case f := <-w.calls:
			f()
			dirty = true
		case <-openAt.C:
			w.exposed = true
		case <-frame.C:
			armed = false
		}
		for len(w.later) > 0 {
			f := w.later[0]
			w.later = w.later[1:]
			f()
		}
		if !w.opened && w.exposed {
			if err := w.openTabs(); err != nil {
				return err
			}
		}
		if w.opened {
			if w.ts.Prune() {
				dirty = true
			}
			if len(w.ts.List) == 0 {
				return nil
			}
		}
		if !dirty || armed || !w.opened {
			continue
		}
		if wait := frameGap - time.Since(lastFrame); wait > 0 {
			frame.Reset(wait)
			armed = true
			continue
		}
		w.paint()
		lastFrame, dirty = time.Now(), false
	}
	return nil
}

func (w *window) xerror(e xgb.Error) {
	if w.xerrs++; w.xerrs <= 5 {
		fmt.Fprintln(os.Stderr, "x11term:", e)
	}
}

// area is the terminal's part of the window, under the strip.
func (w *window) area() image.Point { return image.Pt(w.size.X, max(1, w.size.Y-w.stripH)) }

func (w *window) openTabs() error {
	w.opened = true
	a := w.area()
	specs := append([]glterm.Tab{{Shell: w.opt.Shell, Args: w.opt.Args, Run: w.opt.Run}}, w.opt.Tabs...)
	for i, spec := range specs {
		t, err := glterm.OpenTab(w.opt, spec, w.grid, a.X, a.Y, w.clip, w.invalidate)
		if err != nil {
			if i == 0 {
				return fmt.Errorf("x11term: %w", err)
			}
			fmt.Fprintln(os.Stderr, "progterm: tab:", err)
			continue
		}
		w.ts.Add(t, i == 0)
	}
	return nil
}

func (w *window) newTab() {
	a := w.area()
	t, err := glterm.OpenTab(w.opt, glterm.Tab{Run: w.opt.Run}, w.grid, a.X, a.Y, w.clip, w.invalidate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "progterm: tab:", err)
		return
	}
	w.ts.Add(t, true)
}

// handle acts on an X event, and reports whether the window needs drawing.
func (w *window) handle(ev xgb.Event) bool {
	switch e := ev.(type) {
	case xproto.ExposeEvent:
		w.expose(image.Rect(int(e.X), int(e.Y), int(e.X)+int(e.Width), int(e.Y)+int(e.Height)))
		w.exposed = true
		return true
	case xproto.ConfigureNotifyEvent:
		if e.Window != w.win {
			return false
		}
		if s := image.Pt(int(e.Width), int(e.Height)); s != w.size {
			w.size = s
			a := w.area()
			w.ts.Resize(w.grid, a.X, a.Y)
			return true
		}
	case xproto.KeyPressEvent:
		w.last = e.Time
		return w.key(byte(e.Detail), e.State)
	case xproto.ButtonPressEvent:
		w.last = e.Time
		return w.button(byte(e.Detail), int(e.EventX), int(e.EventY), e.State, true)
	case xproto.ButtonReleaseEvent:
		w.last = e.Time
		return w.button(byte(e.Detail), int(e.EventX), int(e.EventY), e.State, false)
	case xproto.MotionNotifyEvent:
		w.last = e.Time
		if t := w.ts.Active(); t != nil && !w.onStrip {
			return t.Term.Mouse(glterm.MouseEvent{X: float32(e.EventX), Y: float32(int(e.EventY) - w.stripH),
				Button: glterm.ButtonNone, Action: glterm.Move, Mods: w.keys.mods(e.State)})
		}
	case xproto.MappingNotifyEvent:
		if e.Request == xproto.MappingKeyboard || e.Request == xproto.MappingModifier {
			if err := w.loadKeymap(); err != nil {
				fmt.Fprintln(os.Stderr, "x11term: keymap:", err)
			}
		}
	case xproto.ClientMessageEvent:
		if e.Type == w.at.wmProtocols && e.Format == 32 && xproto.Atom(e.Data.Data32[0]) == w.at.wmDelete {
			w.quit = true
		}
	case xproto.SelectionRequestEvent:
		w.clip.request(e)
	case xproto.SelectionNotifyEvent:
		w.clip.notify(e)
	case xproto.SelectionClearEvent:
		delete(w.clip.owned, e.Selection)
	}
	return false
}

// key is a key press of keycode code with state held.
func (w *window) key(code byte, state uint16) bool {
	t := w.ts.Active()
	if t == nil {
		return false
	}
	e, text, ok := translate(w.keys.lookup(code, state), w.keys.mods(state))
	switch {
	case !ok:
		return false
	case text != "":
		return t.Term.Text(text)
	case w.ts.Shortcut(e, w.newTab):
		return true
	}
	return t.Term.Key(e)
}

// button is a press or release of button b at (x, y) in the window.
func (w *window) button(b byte, x, y int, state uint16, press bool) bool {
	t := w.ts.Active()
	if t == nil {
		return false
	}
	bit := uint8(1) << b
	if b > 3 {
		bit = 0 // the wheel holds nothing
	}
	if press {
		if y < w.stripH && w.held == 0 {
			w.onStrip = true
			if bit != 0 {
				w.ts.StripPress(glterm.LayoutStrip(w.ts.Labels(), w.grid.CellW, w.size.X, w.stripH), x, b == 2, w.newTab)
			}
			w.held |= bit
			return true
		}
		w.held |= bit
	} else {
		w.held &^= bit
		if w.onStrip {
			w.onStrip = w.held != 0
			return false
		}
	}
	var btn glterm.Button
	switch b {
	case 1:
		btn = glterm.ButtonLeft
	case 2:
		btn = glterm.ButtonMiddle
	case 3:
		btn = glterm.ButtonRight
	case 4:
		btn = glterm.WheelUp
	case 5:
		btn = glterm.WheelDown
	default:
		return false // a sideways wheel
	}
	if !press && b > 3 {
		return false
	}
	a := glterm.Press
	if !press {
		a = glterm.Release
	}
	return t.Term.Mouse(glterm.MouseEvent{X: float32(x), Y: float32(y - w.stripH), Button: btn, Action: a, Mods: w.keys.mods(state)})
}

// atoms are the atoms the window uses beyond the predefined ones.
type atoms struct {
	clipboard, utf8, targets, text, incr, prop, multiple, timestamp xproto.Atom
	wmProtocols, wmDelete, netWMName, netWMPid                      xproto.Atom
}

func internAtoms(c *xgb.Conn) (atoms, error) {
	var a atoms
	names := []struct {
		name string
		to   *xproto.Atom
	}{
		{"CLIPBOARD", &a.clipboard}, {"UTF8_STRING", &a.utf8}, {"TARGETS", &a.targets}, {"TEXT", &a.text},
		{"INCR", &a.incr}, {"PROGTERM_SELECTION", &a.prop}, {"MULTIPLE", &a.multiple}, {"TIMESTAMP", &a.timestamp},
		{"WM_PROTOCOLS", &a.wmProtocols}, {"WM_DELETE_WINDOW", &a.wmDelete},
		{"_NET_WM_NAME", &a.netWMName}, {"_NET_WM_PID", &a.netWMPid},
	}
	cookies := make([]xproto.InternAtomCookie, len(names))
	for i, n := range names {
		cookies[i] = xproto.InternAtom(c, false, uint16(len(n.name)), n.name) //nolint:gosec // short names
	}
	for i, ck := range cookies {
		r, err := ck.Reply()
		if err != nil {
			return a, err
		}
		*names[i].to = r.Atom
	}
	return a, nil
}

//go:build cgo && unix

// Package gioterm is glterm's terminal in a Gio window: the same session,
// grid, selection and menu, with Gio as the platform layer underneath —
// window, input, clipboard and the on-screen keyboard.
package gioterm

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0magnet/gio/app"
	"github.com/0magnet/gio/f32"
	"github.com/0magnet/gio/io/clipboard"
	"github.com/0magnet/gio/io/event"
	"github.com/0magnet/gio/io/input"
	"github.com/0magnet/gio/io/key"
	"github.com/0magnet/gio/io/pointer"
	"github.com/0magnet/gio/io/transfer"
	"github.com/0magnet/gio/op"
	"github.com/0magnet/gio/op/clip"
	"github.com/0magnet/gio/op/paint"
	"github.com/0magnet/gio/unit"

	"github.com/0magnet/progterm/glterm"
)

// Run opens a Gio window running opt.Shell, and ends the process when the
// shell exits or the window closes: Gio's app.Main owns the main goroutine
// and never returns, so the window runs on another.
func Run(opt glterm.Options) error {
	f, err := glterm.LoadFont(opt.Font)
	if err != nil {
		fmt.Fprintf(os.Stderr, "progterm: font: %v, using %s\n", err, f.Family)
	}
	if opt.FontPt > 0 {
		f.Pt = opt.FontPt
	}
	if opt.Cols <= 0 || opt.Rows <= 0 {
		opt.Cols, opt.Rows = 100, 30
	}
	go func() {
		w := new(app.Window)
		title := opt.Title
		if title == "" {
			title = "progterm"
		}
		// The size at the scale the screen asks for, checked at the first frame.
		size := image.Pt(800, 500)
		scr, scale := screen()
		opts := []app.Option{app.Title(title)}
		if g, err := glterm.NewGridFont(f, f.Pt*glterm.DpPerPt()*float64(scale)); err == nil {
			size = glterm.WindowSize(g, opt.Cols, opt.Rows, scr)
			// A phone's status and navigation bars sit on the window's edges.
			_, bg := g.Colors()
			c := color.NRGBA{R: bg.R, G: bg.G, B: bg.B, A: 0xff}
			opts = append(opts, app.StatusColor(c), app.NavigationColor(c))
		}
		w.Option(append(opts, app.Size(unit.Dp(float32(size.X)/scale), unit.Dp(float32(size.Y)/scale)))...)
		if err := loop(w, opt, f); err != nil {
			fmt.Fprintln(os.Stderr, "progterm:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
	return nil
}

// resizeSettle is how long a window size holds before the terminal takes it:
// the terminal reflows for each size, and a passing size narrower than the
// prompt leaves the cursor where the prompt was cut.
const resizeSettle = 150 * time.Millisecond

// clipboardBridge is glterm's Clipboard on Gio: reads and writes are commands
// a frame executes, so they are queued here and run at the next one. Gio has
// no primary selection; it is kept in the process.
type clipboardBridge struct {
	primary string
	write   *string
	read    func(string)
	w       *app.Window
}

func (c *clipboardBridge) Set(primary bool, text string) {
	if primary {
		c.primary = text
		return
	}
	c.write = &text
	c.w.Invalidate()
}

func (c *clipboardBridge) Get(primary bool, paste func(string)) {
	if primary {
		if c.primary != "" {
			paste(c.primary)
		}
		return
	}
	c.read = paste
	c.w.Invalidate()
}

func loop(w *app.Window, opt glterm.Options, f glterm.Font) error {
	var (
		ops     op.Ops
		tag     = new(int) // the window's input target
		cb      = &clipboardBridge{w: w}
		grid    *glterm.Grid
		pxPerSp float32
		buttons pointer.Buttons // held before the last pointer event
		focused bool
		ts      glterm.Tabs
		area    image.Point // the terminal's part of the window
		// A new area is taken once it has held for resizeSettle, as a phone turning
		// over can pass a frame with the old size and the new insets.
		resizeTo image.Point
		resizeAt time.Time
		bar      *image.RGBA
		// Each frame draws into the other buffer, so the one Gio still reads
		// is left alone.
		bufs  [2]*image.RGBA
		frame int
		sized bool // the window was made its size in cells
		ime   imeMirror
	)
	pending := append([]glterm.Tab{{Shell: opt.Shell, Args: opt.Args, Run: opt.Run}}, opt.Tabs...)
	defer ts.CloseAll()

	open := func(spec glterm.Tab, front bool) error {
		t, err := glterm.OpenTab(opt, spec, grid, area.X, area.Y, cb, w.Invalidate)
		if err != nil {
			return err
		}
		ts.Add(t, front)
		return nil
	}

	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err

		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			// The window less its insets: system bars, and an on-screen keyboard.
			size := gtx.Constraints.Max

			if grid == nil || e.Metric.PxPerSp != pxPerSp {
				g, err := glterm.NewGridFont(f, f.Pt*glterm.DpPerPt()*float64(e.Metric.PxPerSp))
				if err != nil {
					return err
				}
				grid, pxPerSp = g, e.Metric.PxPerSp
				area = image.Point{} // lay the terminals out again at the new size
			}
			if !sized {
				// A wrong guess is put right before a tab opens: a terminal
				// resized under a prompt can lose track of its cursor.
				sized = true
				scr, _ := screen()
				if want := glterm.WindowSize(grid, opt.Cols, opt.Rows, scr); want != size {
					dp := func(px int) unit.Dp { return unit.Dp(float32(px) / e.Metric.PxPerDp) }
					w.Option(app.Size(dp(want.X), dp(want.Y)))
					e.Frame(gtx.Ops)
					w.Invalidate()
					continue
				}
			}
			stripH := glterm.StripHeight(grid)
			if a := image.Pt(size.X, max(1, size.Y-stripH)); a != area {
				if a != resizeTo {
					resizeTo, resizeAt = a, e.Now.Add(resizeSettle)
				}
				// The first layout, or one for a new font, is taken at once.
				if area == (image.Point{}) || !e.Now.Before(resizeAt) {
					area = a
					ts.Resize(grid, area.X, area.Y)
					bufs = [2]*image.RGBA{}
				} else {
					gtx.Execute(op.InvalidateCmd{At: resizeAt})
				}
			}

			for i, spec := range pending {
				if err := open(spec, i == 0); err != nil {
					if i == 0 {
						return fmt.Errorf("gioterm: %w", err)
					}
					fmt.Fprintln(os.Stderr, "progterm: tab:", err)
				}
			}
			pending = nil
			ts.Prune()
			if len(ts.List) == 0 {
				return nil
			}

			s := glterm.LayoutStrip(ts.Labels(), grid.CellW, size.X, stripH)
			newTab := func() {
				if err := open(glterm.Tab{Run: opt.Run}, true); err != nil {
					fmt.Fprintln(os.Stderr, "progterm: tab:", err)
				}
			}

			// The whole window is the input target; the strip takes the
			// pointer above the terminal.
			whole := clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops)
			event.Op(gtx.Ops, tag)
			// No autocorrect or learning: a terminal is typed commands and passwords.
			key.InputHintOp{Tag: tag, Hint: key.HintPassword}.Add(gtx.Ops)
			if !focused {
				gtx.Execute(key.FocusCmd{Tag: tag})
				focused = true
			}
			handle(gtx.Source, tag, &ts, s, stripH, newTab, cb, &buttons, &ime)
			whole.Pop()

			if cb.write != nil {
				gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(*cb.write))})
				cb.write = nil
			}
			if cb.read != nil {
				gtx.Execute(clipboard.ReadCmd{Tag: tag})
			}

			// Input may have opened a tab, so lay the strip out again to draw it.
			s = glterm.LayoutStrip(ts.Labels(), grid.CellW, size.X, stripH)
			if bar == nil || bar.Bounds().Size() != image.Pt(size.X, stripH) {
				bar = image.NewRGBA(image.Rect(0, 0, size.X, stripH))
			}
			// Unclipped, so it is also what shows under the system bars, edge to edge.
			_, bg := grid.Colors()
			paint.Fill(gtx.Ops, color.NRGBA{R: bg.R, G: bg.G, B: bg.B, A: 0xff})
			glterm.DrawStrip(bar, grid, &ts, s)
			paintImage(gtx.Ops, bar, image.Point{})

			if bufs[0] == nil {
				bufs = [2]*image.RGBA{image.NewRGBA(image.Rectangle{Max: area}), image.NewRGBA(image.Rectangle{Max: area})}
			}
			img := bufs[frame%2]
			frame++
			ts.Active().Term.Draw(img)
			paintImage(gtx.Ops, img, image.Pt(0, stripH))
			e.Frame(gtx.Ops)
		}
	}
}

// paintImage paints img with its top left at at.
func paintImage(ops *op.Ops, img *image.RGBA, at image.Point) {
	defer op.Offset(at).Push(ops).Pop()
	defer clip.Rect(img.Bounds()).Push(ops).Pop()
	paint.NewImageOp(img).Add(ops)
	paint.PaintOp{}.Add(ops)
}

// handle reads the frame's input: tab shortcuts and the strip for the window,
// the rest for the current tab's terminal, which sees the pointer relative to
// its own top.
func handle(src input.Source, tag *int, ts *glterm.Tabs, s glterm.Strip, stripH int, newTab func(), cb *clipboardBridge, held *pointer.Buttons, ime *imeMirror) {
	filters := []event.Filter{
		key.FocusFilter{Target: tag},
		// Keys that are not text: named keys, and anything with Ctrl or Alt.
		// Plain typing comes as EditEvents, which an on-screen keyboard and an
		// input method also send.
		key.Filter{Focus: tag, Name: key.NameTab, Optional: key.ModShift | key.ModCtrl},
		key.Filter{Focus: tag, Name: "", Required: key.ModCtrl, Optional: key.ModShift | key.ModAlt},
		key.Filter{Focus: tag, Name: "", Required: key.ModAlt, Optional: key.ModShift},
		pointer.Filter{
			Target:  tag,
			Kinds:   pointer.Press | pointer.Release | pointer.Drag | pointer.Move | pointer.Scroll,
			ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20},
		},
		transfer.TargetFilter{Target: tag, Type: "application/text"},
	}
	for _, n := range []key.Name{key.NameReturn, key.NameEnter, key.NameEscape, key.NameDeleteBackward,
		key.NameDeleteForward, key.NameUpArrow, key.NameDownArrow, key.NameLeftArrow, key.NameRightArrow,
		key.NameHome, key.NameEnd, key.NamePageUp, key.NamePageDown,
		key.NameF1, key.NameF2, key.NameF3, key.NameF4, key.NameF5, key.NameF6,
		key.NameF7, key.NameF8, key.NameF9, key.NameF10, key.NameF11, key.NameF12} {
		filters = append(filters, key.Filter{Focus: tag, Name: n, Optional: key.ModShift | key.ModCtrl | key.ModAlt})
	}
	for {
		ev, ok := src.Event(filters...)
		if !ok {
			return
		}
		t := ts.Active()
		switch ev := ev.(type) {
		case key.EditEvent:
			erase, typed := ime.edit(ev)
			for range erase {
				t.Term.Key(glterm.KeyEvent{Key: glterm.KeyBackspace})
			}
			t.Term.Text(typed)
			ime.report(src.Execute, tag)
		case key.SnippetEvent, key.SelectionEvent:
			ime.report(src.Execute, tag)
		case key.Event:
			if ev.State != key.Press {
				continue
			}
			if ev.Name == key.NameDeleteBackward {
				ime.backspace()
			} else {
				ime.newLine()
			}
			ime.report(src.Execute, tag)
			if ke, ok := termKey(ev); ok && !ts.Shortcut(ke, newTab) {
				t.Term.Key(ke)
			}
		case pointer.Event:
			if ev.Kind == pointer.Press {
				src.Execute(key.FocusCmd{Tag: tag})
				src.Execute(key.SoftKeyboardCmd{Show: true})
			}
			if int(ev.Position.Y) < stripH && *held == 0 {
				if ev.Kind == pointer.Press {
					ts.StripPress(s, int(ev.Position.X), ev.Buttons.Contain(pointer.ButtonTertiary), newTab)
				}
				*held = ev.Buttons
				continue
			}
			ev.Position.Y -= float32(stripH)
			for _, me := range termMouse(ev, held) {
				t.Term.Mouse(me)
			}
		case transfer.DataEvent:
			if paste := cb.read; paste != nil {
				cb.read = nil
				r := ev.Open()
				b, err := io.ReadAll(r)
				r.Close() //nolint:errcheck,gosec // read in full above
				if err == nil && len(b) > 0 {
					paste(string(b))
				}
			}
		}
	}
}

// names maps Gio's key names to glterm's.
var names = map[key.Name]glterm.Key{
	key.NameReturn: glterm.KeyEnter, key.NameEnter: glterm.KeyEnter, key.NameEscape: glterm.KeyEscape,
	key.NameDeleteBackward: glterm.KeyBackspace, key.NameDeleteForward: glterm.KeyDelete,
	key.NameTab: glterm.KeyTab, key.NameUpArrow: glterm.KeyUp, key.NameDownArrow: glterm.KeyDown,
	key.NameLeftArrow: glterm.KeyLeft, key.NameRightArrow: glterm.KeyRight,
	key.NameHome: glterm.KeyHome, key.NameEnd: glterm.KeyEnd, key.NamePageUp: glterm.KeyPageUp, key.NamePageDown: glterm.KeyPageDown,
	key.NameF1: glterm.KeyF1, key.NameF2: glterm.KeyF2, key.NameF3: glterm.KeyF3, key.NameF4: glterm.KeyF4,
	key.NameF5: glterm.KeyF5, key.NameF6: glterm.KeyF6, key.NameF7: glterm.KeyF7, key.NameF8: glterm.KeyF8,
	key.NameF9: glterm.KeyF9, key.NameF10: glterm.KeyF10, key.NameF11: glterm.KeyF11, key.NameF12: glterm.KeyF12,
}

// mods is Gio's modifiers as glterm's.
func mods(m key.Modifiers) glterm.Mods {
	var g glterm.Mods
	if m.Contain(key.ModShift) {
		g |= glterm.Shift
	}
	if m.Contain(key.ModCtrl) {
		g |= glterm.Ctrl
	}
	if m.Contain(key.ModAlt) {
		g |= glterm.Alt
	}
	if m.Contain(key.ModSuper) {
		g |= glterm.Meta
	}
	return g
}

// termKey is a Gio key press as glterm's KeyEvent.
func termKey(e key.Event) (glterm.KeyEvent, bool) {
	ke := glterm.KeyEvent{Mods: mods(e.Modifiers)}
	if e.Name == key.NameSpace {
		ke.Rune = ' '
		return ke, true
	}
	if k, ok := names[e.Name]; ok {
		ke.Key = k
		return ke, true
	}
	// A letter, digit or symbol held with Ctrl or Alt: Gio names it by the
	// character, upper case for letters.
	r, n := utf8.DecodeRuneInString(string(e.Name))
	if n == 0 || n != len(e.Name) {
		return ke, false
	}
	if r >= 'A' && r <= 'Z' && !e.Modifiers.Contain(key.ModShift) {
		r += 'a' - 'A'
	}
	ke.Rune = r
	return ke, true
}

// termMouse is a Gio pointer event as glterm's MouseEvents. Gio reports which
// buttons are held; a press or release is of the ones that changed.
func termMouse(e pointer.Event, held *pointer.Buttons) []glterm.MouseEvent {
	at := func(b glterm.Button, a glterm.Action) glterm.MouseEvent {
		return glterm.MouseEvent{X: e.Position.X, Y: e.Position.Y, Button: b, Action: a, Mods: mods(e.Modifiers)}
	}
	var out []glterm.MouseEvent
	switch e.Kind {
	case pointer.Press, pointer.Release:
		changed := e.Buttons ^ *held
		a := glterm.Press
		if e.Kind == pointer.Release {
			a = glterm.Release
		}
		for _, b := range []struct {
			g pointer.Buttons
			t glterm.Button
		}{{pointer.ButtonPrimary, glterm.ButtonLeft}, {pointer.ButtonSecondary, glterm.ButtonRight}, {pointer.ButtonTertiary, glterm.ButtonMiddle}} {
			if changed&b.g != 0 {
				out = append(out, at(b.t, a))
			}
		}
		if e.Source == pointer.Touch && len(out) == 0 {
			out = append(out, at(glterm.ButtonLeft, a))
		}
	case pointer.Drag, pointer.Move:
		out = append(out, at(glterm.ButtonNone, glterm.Move))
	case pointer.Scroll:
		if e.Scroll != (f32.Point{}) {
			b := glterm.WheelDown
			if e.Scroll.Y < 0 {
				b = glterm.WheelUp
			}
			out = append(out, at(b, glterm.Press))
		}
	}
	*held = e.Buttons
	return out
}

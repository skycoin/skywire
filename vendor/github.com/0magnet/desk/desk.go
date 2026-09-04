//go:build js && wasm

// Package desk arranges panes in windows.
//
// It is deliberately small, and knows nothing about terminals, images or
// anything else it might end up holding. winbox-go supplies the windows; a
// pane is anything that can render into a DOM element. Programs register the
// panes they have and ask for one by name; the desk creates a window, mounts
// the pane in it, and cleans up when the window closes.
//
// Keeping the shell ignorant of its contents is the point. A terminal is one
// pane, an image viewer is another, and neither had to be anticipated here —
// which is what lets a project bring its own pane rather than waiting for this
// package to grow support for it.
package desk

import (
	"fmt"
	"sort"
	"sync"
	"syscall/js"

	winbox "github.com/0magnet/winbox-go"
)

// Pane is something that can live in a window.
type Pane interface {
	// Mount renders the pane into el, the window's content element. The
	// element is empty and sized before Mount is called.
	Mount(el js.Value) error

	// Close releases whatever Mount acquired. It is called when the window
	// closes, and must tolerate a pane that failed to mount or never did.
	Close()
}

// Resizer is implemented by panes that need to be told their size changed.
//
// Most do not. A pane laid out with CSS is resized by the browser, and one that
// watches its own element — as a terminal does, since it has to convert pixels
// into a character grid — hears about it from a ResizeObserver without anyone
// passing the message along. Implement this only when neither is true.
type Resizer interface {
	Resize(width, height float64)
}

// TexturePane is implemented by a pane whose pixels all live in one canvas.
//
// It exists because the DOM cannot be sampled into a WebGL texture and a canvas
// can: texImage2D takes an HTMLCanvasElement directly. That single asymmetry is
// what decides which panes the WebGL compositor can draw, so rather than have
// the desk guess — hunt for a canvas in the pane's subtree, and be wrong about
// the one pane that has two — a pane says so itself.
//
// Implement it only if the canvas really is the whole pane. A pane that draws
// into a canvas and puts a toolbar beside it must not: the toolbar would be
// left out of the picture. Implementing it costs nothing when the compositor is
// off, which is the default — see EnableCompositing.
//
// Returning a zero or undefined value, or a canvas that has not been sized yet,
// is allowed and means "not now": that pane stays on the DOM path for as long
// as it keeps saying so, and is picked up on the first frame it does not.
type TexturePane interface {
	Canvas() js.Value
}

// App is a registered pane type: what to call it, and how to make one.
type App struct {
	Name  string // the identifier passed to Launch
	Title string // the window title
	Icon  string // optional icon URL
	Help  string // one line, for a launcher

	// Width and Height are the preferred window size in pixels. Zero picks a
	// default. Both are capped at what the desk can actually show, so this is
	// what the app would like rather than what it will get.
	Width, Height float64

	// Maximized opens the window filling the desk. Worth setting for whatever
	// a page opens with: a small window adrift in a large empty desktop reads
	// as something that failed to size itself, not as a window manager.
	Maximized bool

	// Open builds a pane. args are whatever Launch was given, so an app can
	// take a filename or a mode without the desk knowing what either means.
	Open func(args []string) (Pane, error)

	// Run is an alternative to Open for an entry that is NOT a window the desk
	// owns: it is called instead, and no pane is built and no window opened.
	//
	// It exists for the case of an application that is hosting the desk rather
	// than being hosted by it — chaosrack keeps its control panel in a window
	// of its own, so it can appear in the launcher beside real desk apps
	// without pretending the desk created it. Set one or the other; Run wins if
	// both are set, since an app that can do its own thing has no use for a
	// pane the desk would wrap it in.
	Run func(args []string) error
}

var (
	mu   sync.Mutex
	apps = map[string]App{}
	root js.Value // where windows are placed; zero means the document body
)

// SetRoot confines windows to an element. Without it winbox places them
// against the body, which is wrong the moment the page has a header: windows
// can be dragged up underneath it and their maximized size is a header too
// tall.
func SetRoot(el js.Value) {
	mu.Lock()
	defer mu.Unlock()
	root = el
}

// Register adds an app, replacing any with the same name.
func Register(a App) {
	mu.Lock()
	defer mu.Unlock()
	apps[a.Name] = a
}

// Apps lists the registered apps by name.
func Apps() []App {
	mu.Lock()
	defer mu.Unlock()
	out := make([]App, 0, len(apps))
	for _, a := range apps {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup finds a registered app.
func Lookup(name string) (App, bool) {
	mu.Lock()
	defer mu.Unlock()
	a, ok := apps[name]
	return a, ok
}

// Options adjust a single launch. Sizes and positions are pixels; zero means
// "use the app's preference", and for X and Y "place it automatically".
type Options struct {
	Title         string // overrides the app's title
	Width, Height float64
	X, Y          float64

	// Maximized overrides the app's own setting when true.
	Maximized bool
}

// Launch opens a window holding a new pane of the named app.
func Launch(name string, args ...string) (*winbox.WinBox, error) {
	return LaunchOpts(name, Options{}, args...)
}

// LaunchOpts is Launch with the window's placement spelled out.
func LaunchOpts(name string, opt Options, args ...string) (*winbox.WinBox, error) {
	app, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("desk: no app named %q", name)
	}
	// An entry that runs itself. Nothing is mounted and no window is made, so
	// there is no WinBox to hand back — the caller gets nil and no error, which
	// is the honest answer to "which window did that open".
	if app.Run != nil {
		return nil, app.Run(args)
	}
	if app.Open == nil {
		return nil, fmt.Errorf("desk: app %q has neither Open nor Run", name)
	}
	pane, err := app.Open(args)
	if err != nil {
		return nil, err
	}

	title := app.Title
	if opt.Title != "" {
		title = opt.Title
	}
	w, h := app.Width, app.Height
	if opt.Width > 0 {
		w = opt.Width
	}
	if opt.Height > 0 {
		h = opt.Height
	}
	if w <= 0 {
		w = 720
	}
	if h <= 0 {
		h = 460
	}

	mu.Lock()
	r := root
	mu.Unlock()

	// An app asks for the size it would like, not the size it can have. On a
	// phone the desk is narrower than any sensible default, and a window
	// opened at its preferred width would hang off the right edge with its
	// own controls out of reach.
	//
	// Where it goes has to be settled first: a window is clamped to the room
	// left after its offset, not to the whole desk, or a cascaded window
	// overflows by exactly the amount it was staggered.
	// Windows are position:fixed, so their coordinates are the viewport's and
	// not the root element's — appending into the root sets parentage, not
	// where the window lands. The root's offset therefore has to be added
	// here, and declared to winbox as a viewport limit so a window cannot be
	// dragged out over the page's own furniture.
	dk := deskRect(r)
	x, y := opt.X, opt.Y
	if x <= 0 && y <= 0 {
		x, y = cascade(dk.w, dk.h)
	}
	w = clamp(w, dk.w-x-8)
	h = clamp(h, dk.h-y-PanelHeight-8)
	x += dk.left
	y += dk.top

	// Keep windows inside the desk, and clear of the panel so one dragged to
	// the bottom does not end up with its controls under the task buttons.
	bottomLimit := dk.bottom
	if panel != nil {
		bottomLimit += PanelHeight
	}

	o := &winbox.Options{
		Root: r,
		// Maximizing respects the viewport limits below, so it fills the desk
		// rather than the page.
		Max:    app.Maximized || opt.Maximized,
		Top:    winbox.Px(dk.top),
		Left:   winbox.Px(dk.left),
		Right:  winbox.Px(dk.right),
		Bottom: winbox.Px(bottomLimit),
		Title:  title,
		Icon:   app.Icon,
		Width:  winbox.Px(w),
		Height: winbox.Px(h),
		// The pane owns the body's contents; nothing here should scroll it.
		Background: "#1b1f27",
	}
	o.X, o.Y = winbox.Px(x), winbox.Px(y)

	win := winbox.New(o)

	// Mount after creation: the body exists and has been sized by now, which
	// a pane that measures itself in pixels depends on.
	if err := pane.Mount(win.Body); err != nil {
		win.Close(true)
		pane.Close()
		return nil, err
	}

	if r, ok := pane.(Resizer); ok {
		win.OnResize = func(_ *winbox.WinBox, width, height float64) {
			r.Resize(width, height)
		}
	}

	// Tracked whether or not the WebGL compositor is running: what it can draw
	// depends on the windows it cannot, and a window that existed before
	// EnableCompositing was called has to be as visible to it as one opened
	// after. See compositor.go.
	lw := trackWindow(win, pane)
	prevTrack := win.OnClose
	win.OnClose = func(wb *winbox.WinBox, force bool) bool {
		if prevTrack != nil && prevTrack(wb, force) {
			return true
		}
		untrackWindow(lw)
		return false
	}

	if panel != nil {
		alive := true
		tracked := &Window{
			Title: title,
			// Show before Restore: minimizing hides the window outright (see
			// OnMinimize below), and restoring something still hidden is a
			// no-op that looks like a dead button.
			Focus: func() { win.Show().Restore().Focus() },
			Alive: func() bool { return alive },
		}

		// A MINIMIZED WINDOW IS REPRESENTED BY ITS TASK BUTTON, which is what
		// every paneled desktop does and what winbox on its own does not:
		// winbox parks a minimized window as a title-bar stub along the bottom
		// of the screen. That is the right answer for a desktop with no panel,
		// because the stub is then the only way back — and the wrong one here,
		// where it means the same window is on screen twice, once as a button
		// and once as a stub sitting on top of the panel.
		//
		// Only inside this branch, and deliberately: with no panel there is no
		// button, and hiding on minimize would put the window somewhere the
		// person cannot reach it.
		prevMin := win.OnMinimize
		win.OnMinimize = func(wb *winbox.WinBox) {
			if prevMin != nil {
				prevMin(wb)
			}
			wb.Hide()
		}
		panel.Track(tracked)
		panel.SetActive(tracked) // a window opens focused

		prevFocus := win.OnFocus
		win.OnFocus = func(wb *winbox.WinBox) {
			if prevFocus != nil {
				prevFocus(wb)
			}
			panel.SetActive(tracked)
		}

		prev := win.OnClose
		win.OnClose = func(wb *winbox.WinBox, force bool) bool {
			if prev != nil && prev(wb, force) {
				return true
			}
			alive = false
			panel.SetActive(nil)
			return false
		}
	}

	prevClose := win.OnClose
	win.OnClose = func(wb *winbox.WinBox, force bool) bool {
		if prevClose != nil && prevClose(wb, force) {
			return true // a handler vetoed it; the pane stays alive
		}
		pane.Close()
		return false
	}
	return win, nil
}

// deskBox is where the desk sits in the viewport, and how much of the viewport
// is not it.
type deskBox struct {
	left, top     float64 // the desk's own offset
	right, bottom float64 // what is left of the viewport past its far edges
	w, h          float64
}

// deskRect measures the desk. Everything about window placement is expressed
// against the viewport, so a desk that is not the whole page has to say by how
// much.
func deskRect(r js.Value) deskBox {
	el := r
	if !el.Truthy() {
		el = js.Global().Get("document").Get("body")
	}
	rect := el.Call("getBoundingClientRect")
	w, h := rect.Get("width").Float(), rect.Get("height").Float()
	if w <= 0 || h <= 0 {
		return deskBox{w: 1024, h: 700} // nothing laid out yet; guess generously
	}
	win := js.Global()
	vw, vh := win.Get("innerWidth").Float(), win.Get("innerHeight").Float()
	return deskBox{
		left:   rect.Get("left").Float(),
		top:    rect.Get("top").Float(),
		right:  vw - rect.Get("right").Float(),
		bottom: vh - rect.Get("bottom").Float(),
		w:      w,
		h:      h,
	}
}

// clamp caps a size at what is available, with a floor so a window is never
// shrunk to something unusable on a very small screen — better to overflow a
// little than to be a sliver.
func clamp(v, max float64) float64 {
	if max < 220 {
		max = 220
	}
	if v > max {
		return max
	}
	return v
}

// cascade staggers new windows so a second one does not land exactly on the
// first, and gives up staggering when there is no room to stagger into.
var cascadeN int

func cascade(availW, availH float64) (float64, float64) {
	if availW < 700 || availH < 520 {
		return 8, 8 // no room to stagger into
	}
	const step = 28
	n := cascadeN % 8
	cascadeN++
	return float64(60 + n*step), float64(50 + n*step)
}

// title is the window's title text, read back from its own chrome.
//
// Read rather than remembered because winbox owns it: SetTitle changes the DOM
// and nothing tells the desk. The compositor only asks when it is drawing the
// chrome itself, which is not every frame and not at all by default.
func (lw *liveWindow) title() string {
	if lw.win == nil || !lw.win.DOM.Truthy() {
		return ""
	}
	if lw.win.DOM.Get("querySelector").Type() != js.TypeFunction {
		return ""
	}
	if t := lw.win.DOM.Call("querySelector", ".wb-title"); t.Truthy() {
		return t.Get("textContent").String()
	}
	return ""
}

//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/desk_tour_js.go c4-wasm-desk
//
// The desk tour: a guided walk through the DESKTOP a visor serves — the
// launcher, the mesh browser, the shell, the shared filesystem, mail, the
// tab's identity, pairing, settings and install.
//
// It is deliberately NOT the tour of the hypervisor dashboard. That dashboard
// is an Angular app which the desk renders as a browser tab (vnet:8001), and it
// carries its own tour, launched from the ? button in its corner. The seam
// between the two is the subject of one step here, and of docs/tours.md.
//
// A tour of a window manager does not need an overlay. Every step names an app
// and the tour OPENS it, so the thing being described is on screen next to the
// text, in a real window the reader can poke at. Moving on closes what the
// step opened — but only if the reader has not taken it over: a window they
// moved, resized or typed into is theirs, and the tour leaves it alone.
package deskhost

import (
	"strconv"
	"syscall/js"

	"github.com/0magnet/desk"
	winbox "github.com/0magnet/winbox-go"
)

const tourAppName = "tour"

func registerTourApp() {
	desk.Register(desk.App{
		Name: tourAppName, Title: "tour",
		Help:   "a guided walk through this desktop",
		Width:  460,
		Height: 430,
		Open:   func(_ []string) (desk.Pane, error) { return &tourPane{}, nil },
	})
}

// tourStep is one screen of the walk. body is a small HTML fragment — the same
// vocabulary the other panes use, so <b>, <i> and <code> are all it needs.
type tourStep struct {
	title string
	body  string

	// app, when set, is the desk app this step is about. Entering the step
	// opens it; leaving closes it again unless the reader has touched it.
	app  string
	args []string
}

// The copy narrates rather than instructs — it describes what a thing is, not
// what the reader should do with it. This tour carries the claim the dashboard
// tour deliberately does not make: a visor running in a browser tab, with a
// desktop around it, is an unusual thing. The Angular dashboard next door is an
// ordinary admin console and says so.
func tourSteps() []tourStep {
	return []tourStep{
		{
			title: "A visor, running in a browser tab",
			body: "Not a page describing Skywire, and not a remote session: the " +
				"<b>wasm binary serving this page is a full visor</b>, routing on the mesh " +
				"from inside this tab. Around it is a <b>desk</b> — a real window manager " +
				"whose windows move, resize, dock and stack. Nothing was installed. " +
				"Nothing is running on a server on the reader's behalf. Everything in " +
				"the following windows executes here.",
		},
		{
			title: "The launcher",
			body: "Every app opens from the <b>launcher</b> in the taskbar, this tour " +
				"included, so it can be closed and reopened. From here each step " +
				"<b>opens the app it describes</b>, beside this window. Windows that get " +
				"moved or resized are left alone afterwards; untouched ones are tidied up.",
		},
		{
			title: "browser — two networks at once",
			body: "<b>netscrape</b> resolves two kinds of address. A " +
				"<code>&lt;pk&gt;.dmsg</code> address fetches a site directly from another " +
				"visor over dmsg: no DNS, no certificate authority, the <b>public key is " +
				"the address and the authentication at once</b>. A clearnet address is " +
				"fetched by an <b>exit visor</b> instead — selected automatically unless one " +
				"is pinned — so the site sees the exit's address. Names like " +
				"<code>skywire.dmsg</code> are a local convenience, not a namespace anyone " +
				"can squat.",
			app: "browser",
		},
		{
			title: "The dashboard is a tab in it",
			body: "The <b>hypervisor dashboard</b> — visor list, transports, routing, the " +
				"mesh-wide views — is not a window here. It is an Angular app this visor " +
				"serves on its virtual loopback, opened as a native tab at " +
				"<code>vnet:8001</code>. That is the seam. It is ordinary admin software, " +
				"which is why it gets a separate, plainer tour, behind the <b>?</b> button " +
				"in its bottom-right corner.",
		},
		{
			title: "console — a shell with no server",
			body: "The console is <b>websh</b>, running in the same wasm runtime as the " +
				"visor. There is no host on the other end of it. Pipes, globbing, control " +
				"flow, job control, <code>jq</code> and <code>awk</code> all work, and the " +
				"visor's own commands emit JSON into the pipeline — the same scripting " +
				"surface <code>skywire cli</code> gives a native visor.",
			app: "console",
		},
		{
			title: "files — one filesystem, two views",
			body: "The file browser and the shell share a single in-memory filesystem. " +
				"<code>echo hi &gt; /notes.txt</code> in the console appears here; an edit " +
				"here is visible to <code>cat</code>. Nothing touches the host disk, and " +
				"like the tab itself it is ephemeral unless exported.",
			app: "files",
		},
		{
			title: "mail — addressed by key",
			body: "A mailbox whose address is a <b>public key</b>, delivered across the " +
				"mesh rather than through a provider. There is no account to register and " +
				"no server holding the messages; a whitelist decides who can deliver.",
			app: "mail",
		},
		{
			title: "identity — the key is the visor",
			body: "This tab's visor <i>is</i> a keypair, and this is where it lives. " +
				"<b>Export</b> backs it up or moves the visor to another device; importing " +
				"one and reloading restarts the visor under that key. The secret half " +
				"leaves the tab only as copied text. There is nobody to recover it from.",
			app: "identity",
		},
		{
			title: "pair — driving the host visor",
			body: "Pairing asks the visor serving this page to accept <b>this tab</b> as its " +
				"hypervisor. The operator approves a fingerprint shown by <code>skywire cli " +
				"visor hv pair</code>, or hands over a one-time code. After that the " +
				"dashboard tab is managing a real visor on the host.",
			app: "pair",
		},
		{
			title: "settings — what each reload builds",
			body: "This tab's <code>skywire.conf</code>: the services it points at and the " +
				"options every reload generates the visor's config from. A browser visor is " +
				"rebuilt from this file on each start, so changes have to land here to " +
				"survive one.",
			app: "settings",
		},
		{
			title: "install — surviving the address",
			body: "Installed as an app, the desk opens on its own from the browser's " +
				"storage and keeps working when the address that served it is unreachable. " +
				"For a tool whose job is browsing a mesh, not needing the network in order " +
				"to start is most of the point.",
			app: installAppName,
		},
		{
			title: "Ephemeral by default",
			body: "No install, no account, no server: a visor, a desktop, a shell, a " +
				"browser and a mailbox in one tab, and all of it gone when the tab closes " +
				"unless the key was exported or the desk installed.<br><br>" +
				"This tour reopens from the launcher. The <b>dashboard</b> has its own, " +
				"behind the <b>?</b> button in its corner.",
		},
	}
}

// tourPane renders one step at a time and owns whatever window the current step
// opened.
type tourPane struct {
	steps []tourStep
	i     int

	doc  js.Value
	root js.Value

	// opened is the window the current step launched, and openedAt the state it
	// was in when we launched it. If the window still looks that way when we
	// leave the step, the reader never touched it and it is ours to close.
	opened   *winbox.WinBox
	openedAt windowMark

	funcs []js.Func
}

// windowMark is the little of a window's state that tells us whether the reader
// has adopted it. Position and size cover moving, resizing and docking; the
// three flags cover minimizing, maximizing and going full-screen. Between them
// that is every way the desk lets someone claim a window.
type windowMark struct {
	x, y, w, h       float64
	min, max, fullsc bool
}

func markOf(w *winbox.WinBox) windowMark {
	if w == nil {
		return windowMark{}
	}

	return windowMark{
		x: w.X, y: w.Y, w: w.Width, h: w.Height,
		min: w.Min, max: w.Max, fullsc: w.Full,
	}
}

func (p *tourPane) Mount(el js.Value) error {
	p.doc = js.Global().Get("document")
	p.root = el
	p.steps = tourSteps()
	p.root.Get("style").Set("cssText", paneCSS)
	p.render()

	return nil
}

func (p *tourPane) Close() {
	p.closeOpened()
	for _, f := range p.funcs {
		f.Release()
	}
	p.funcs = nil
}

// closeOpened drops the window the current step opened, unless the reader has
// moved or resized it since. force is never passed: a pane that refuses to
// close (an editor with unsaved work, say) has a reason, and the tour is not it.
func (p *tourPane) closeOpened() {
	if p.opened == nil {
		return
	}
	if markOf(p.opened) == p.openedAt {
		p.opened.Close(false)
	}
	p.opened = nil
}

func (p *tourPane) go2(n int) {
	if n < 0 || n >= len(p.steps) {
		return
	}
	p.closeOpened()
	p.i = n
	if app := p.steps[n].app; app != "" {
		// A step whose app is not registered is not a failure. Not every desk
		// registers every app — the terminal needs a host serving the page, and
		// install is only offered where the desk can be installed — so the step
		// still renders and its text stands on its own without the window.
		if w, err := desk.Launch(app, p.steps[n].args...); err == nil && w != nil {
			p.opened = w
			p.openedAt = markOf(w)
		}
	}
	p.render()
}

func (p *tourPane) render() {
	step := p.steps[p.i]
	p.root.Set("innerHTML", "")

	count := paneEl(p.doc, "div", "float:right;opacity:.45;font-size:12px;letter-spacing:.04em", "")
	count.Set("textContent", strconv.Itoa(p.i+1)+" / "+strconv.Itoa(len(p.steps)))
	p.root.Call("appendChild", count)

	h := paneEl(p.doc, "h3", "margin:.1em 0 .55em;font-size:16px;color:#fff", step.title)
	p.root.Call("appendChild", h)

	body := paneEl(p.doc, "div", "opacity:.92", "")
	body.Set("innerHTML", step.body)
	p.root.Call("appendChild", body)

	row := paneEl(p.doc, "div", "display:flex;gap:8px;margin-top:1.1em;align-items:center", "")
	if p.i > 0 {
		row.Call("appendChild", p.button("Back", func() { p.go2(p.i - 1) }))
	}
	row.Call("appendChild", paneEl(p.doc, "span", "flex:1", ""))
	if p.i < len(p.steps)-1 {
		row.Call("appendChild", p.button("Next", func() { p.go2(p.i + 1) }))
	} else {
		// No "Done" button: this is a window, and the way to be done with a
		// window is its own close control. Saying so beats growing a second one.
		row.Call("appendChild", paneEl(p.doc,
			"span", "opacity:.5;font-size:12px", "close this window when you're done"))
	}
	p.root.Call("appendChild", row)
}

// button keeps its js.Func so Close can release it; paneButton allocates one
// per call and this pane rebuilds its buttons on every step.
func (p *tourPane) button(label string, onClick func()) js.Value {
	b := paneEl(p.doc, "button",
		"padding:6px 15px;font:inherit;cursor:pointer;background:#1d2632;color:#dbe6f3;"+
			"border:1px solid #33404f;border-radius:6px", label)
	fn := js.FuncOf(func(js.Value, []js.Value) any {
		go onClick()
		return nil
	})
	p.funcs = append(p.funcs, fn)
	b.Call("addEventListener", "click", fn)

	return b
}

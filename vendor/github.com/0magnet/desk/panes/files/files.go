//go:build js && wasm

// Package files is a file manager over the shared filesystem.
//
// It is the other half of having a terminal: the shell is better at doing
// things to files, and a list is better at finding out what is there. Both work
// on the same filesystem, so a file written by a command appears here on the
// next refresh, and opening something here is the same `view` the shell has.
package files

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"syscall/js"
	"time"

	"github.com/0magnet/afero"

	"github.com/0magnet/desk"
	"github.com/0magnet/desk/dom"
)

const css = `
.dfm { display:flex; flex-direction:column; height:100%; background:#1b1f27;
       color:#d3d7cf; font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace; }
.dfm-bar { flex:0 0 auto; display:flex; align-items:center; gap:6px;
           padding:6px 8px; background:#232833; border-bottom:1px solid #2e3540; }
.dfm-path { flex:1 1 auto; overflow:hidden; text-overflow:ellipsis;
            white-space:nowrap; color:#8fc6f0; }
.dfm-btn { cursor:pointer; padding:2px 8px; border-radius:4px; user-select:none;
           background:#2e3540; color:#d3d7cf; }
.dfm-btn:hover { background:#3b434f; }
.dfm-list { flex:1 1 auto; overflow:auto; min-height:0; }
.dfm-row { display:flex; gap:8px; padding:3px 10px; cursor:default; }
.dfm-row:hover { background:#252b36; }
.dfm-row.dir .dfm-name { color:#8fc6f0; }
.dfm-name { flex:1 1 auto; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.dfm-size { flex:0 0 auto; color:#7d8694; }
.dfm-time { flex:0 0 auto; color:#5b6472; width:11ch; text-align:right; }
.dfm-empty { padding:14px; color:#7d8694; }
.dfm-row.sel { background:#2f3949; }
.dfm-menu { position:fixed; z-index:100002; min-width:150px; padding:4px;
            background:#1b2028; border:1px solid #2f3744; border-radius:6px;
            box-shadow:0 10px 28px rgba(0,0,0,.55);
            font:13px/1 ui-sans-serif,system-ui,sans-serif; }
.dfm-menu[hidden] { display:none; }
.dfm-mi { padding:7px 10px; border-radius:4px; cursor:pointer; color:#d3d7cf; }
.dfm-mi:hover { background:#2a3242; }
.dfm-mi.danger:hover { background:#5a2a2a; color:#ffd7d7; }
.dfm-err { padding:6px 10px; color:#e88; background:#2a1e1e;
           border-bottom:1px solid #3a2a2a; }
`

// Pane lists a directory.
type Pane struct {
	fs  afero.Fs
	dir string

	root, list, pathEl, menu, errEl js.Value
	fns                             dom.Funcs
	menuTarget                      string
	menuIsDir                       bool
}

// New opens at dir, or /home/user if empty.
func New(fs afero.Fs, dir string) *Pane {
	if dir == "" {
		dir = "/home/user"
	}
	return &Pane{fs: fs, dir: dir}
}

// Mount builds the listing.
func (p *Pane) Mount(el js.Value) error {
	dom.Stylesheet("desk-files-css", css)

	p.pathEl = dom.El("div", dom.Class("dfm-path"))
	p.list = dom.El("div", dom.Class("dfm-list"))

	up := dom.El("div", dom.Class("dfm-btn"), dom.Text("up"),
		p.fns.On("click", func(js.Value) { p.chdir(path.Dir(p.dir)) }))
	home := dom.El("div", dom.Class("dfm-btn"), dom.Text("home"),
		p.fns.On("click", func(js.Value) { p.chdir("/home/user") }))
	mkdir := dom.El("div", dom.Class("dfm-btn"), dom.Text("new folder"),
		p.fns.On("click", func(js.Value) { p.mkdir() }))
	refresh := dom.El("div", dom.Class("dfm-btn"), dom.Text("refresh"),
		p.fns.On("click", func(js.Value) { p.render() }))

	p.errEl = dom.El("div", dom.Class("dfm-err"), dom.Attr("hidden", ""))
	p.menu = p.buildMenu()

	p.root = dom.El("div", dom.Class("dfm"),
		dom.Child(dom.El("div", dom.Class("dfm-bar"),
			dom.Child(up), dom.Child(home), dom.Child(p.pathEl),
			dom.Child(mkdir), dom.Child(refresh))),
		dom.Child(p.errEl),
		dom.Child(p.list),
		dom.Child(p.menu))

	el.Call("appendChild", p.root)
	// Any click that is not on the menu closes it. Through OnTarget so that
	// closing the pane takes the listener off document with it — this used to
	// be a raw js.FuncOf that Release never saw, so every file manager ever
	// opened went on hiding the menu of a pane that no longer existed.
	p.fns.OnTarget(js.Global().Get("document"), "click", func(js.Value) { p.hideMenu() })

	// The shared filesystem can be REPLACED under a pane that is already
	// listing a directory: with --auth the token arrives after the page is
	// running, and the desk swaps memory for the machine the moment it does.
	// Nothing about this listing changes on its own, so without this the
	// window keeps showing the in-memory filesystem and looks like the token
	// did not work.
	p.fns.OnTarget(js.Global(), dom.FSChangedEvent, func(js.Value) { p.render() })

	p.render()
	return nil
}

// buildMenu makes the one context menu the pane reuses, rather than creating
// and destroying one per right-click.
func (p *Pane) buildMenu() js.Value {
	item := func(label, class string, fn func()) js.Value {
		return dom.El("div", dom.Class("dfm-mi "+class), dom.Text(label),
			p.fns.On("click", func(ev js.Value) {
				ev.Call("stopPropagation")
				p.hideMenu()
				fn()
			}))
	}
	return dom.El("div", dom.Class("dfm-menu"), dom.Attr("hidden", ""),
		dom.Child(item("Open", "", func() { p.open(p.menuTarget, p.menuIsDir) })),
		dom.Child(item("Rename…", "", func() { p.rename(p.menuTarget) })),
		dom.Child(item("Delete", "danger", func() { p.remove(p.menuTarget, p.menuIsDir) })),
		p.fns.On("click", func(ev js.Value) { ev.Call("stopPropagation") }))
}

func (p *Pane) showMenu(x, y float64, target string, isDir bool) {
	p.menuTarget, p.menuIsDir = target, isDir
	st := p.menu.Get("style")
	st.Set("left", fmt.Sprintf("%.0fpx", x))
	st.Set("top", fmt.Sprintf("%.0fpx", y))
	p.menu.Call("removeAttribute", "hidden")
}

func (p *Pane) hideMenu() { p.menu.Call("setAttribute", "hidden", "") }

// fail shows an error in the pane rather than a dialog, so a failed rename does
// not also take the window's focus away.
func (p *Pane) fail(err error) {
	if err == nil {
		p.errEl.Call("setAttribute", "hidden", "")
		return
	}
	p.errEl.Set("textContent", err.Error())
	p.errEl.Call("removeAttribute", "hidden")
}

func (p *Pane) mkdir() {
	name := prompt("New folder name", "")
	if name == "" {
		return
	}
	if err := p.fs.MkdirAll(path.Join(p.dir, name), 0o755); err != nil {
		p.fail(err)
		return
	}
	p.fail(nil)
	p.render()
}

func (p *Pane) rename(full string) {
	old := path.Base(full)
	name := prompt("Rename "+old+" to", old)
	if name == "" || name == old {
		return
	}
	if err := p.fs.Rename(full, path.Join(path.Dir(full), name)); err != nil {
		p.fail(err)
		return
	}
	p.fail(nil)
	p.render()
}

func (p *Pane) remove(full string, isDir bool) {
	what := "Delete " + path.Base(full) + "?"
	if isDir {
		what = "Delete the folder " + path.Base(full) + " and everything in it?"
	}
	if !confirm(what) {
		return
	}
	var err error
	if isDir {
		err = p.fs.RemoveAll(full)
	} else {
		err = p.fs.Remove(full)
	}
	if err != nil {
		p.fail(err)
		return
	}
	p.fail(nil)
	p.render()
}

func prompt(message, def string) string {
	v := js.Global().Call("prompt", message, def)
	if !v.Truthy() {
		return ""
	}
	return strings.TrimSpace(v.String())
}

func confirm(message string) bool {
	return js.Global().Call("confirm", message).Bool()
}

func (p *Pane) chdir(dir string) {
	if dir == "" {
		dir = "/"
	}
	p.dir = path.Clean(dir)
	p.render()
}

func (p *Pane) render() {
	p.pathEl.Set("textContent", p.dir)
	dom.Clear(p.list)

	infos, err := afero.ReadDir(p.fs, p.dir)
	if err != nil {
		p.list.Call("appendChild",
			dom.El("div", dom.Class("dfm-empty"), dom.Text(err.Error())))
		return
	}
	// Directories first, then names — the order a person expects rather than
	// the order the filesystem happens to return.
	sort.SliceStable(infos, func(i, j int) bool {
		if infos[i].IsDir() != infos[j].IsDir() {
			return infos[i].IsDir()
		}
		return infos[i].Name() < infos[j].Name()
	})
	if len(infos) == 0 {
		p.list.Call("appendChild",
			dom.El("div", dom.Class("dfm-empty"), dom.Text("empty")))
		return
	}

	for _, info := range infos {
		name, isDir, full := info.Name(), info.IsDir(), path.Join(p.dir, info.Name())
		class := "dfm-row"
		label := name
		size := humanSize(info.Size())
		if isDir {
			class += " dir"
			label = name + "/"
			size = ""
		}
		row := dom.El("div", dom.Class(class),
			dom.Child(dom.El("span", dom.Class("dfm-name"), dom.Text(label))),
			dom.Child(dom.El("span", dom.Class("dfm-size"), dom.Text(size))),
			dom.Child(dom.El("span", dom.Class("dfm-time"),
				dom.Text(info.ModTime().Format(time.RFC3339[:10])))),
			p.fns.On("dblclick", func(js.Value) { p.open(full, isDir) }),
			p.fns.On("contextmenu", func(ev js.Value) {
				ev.Call("preventDefault")
				ev.Call("stopPropagation")
				p.showMenu(ev.Get("clientX").Float(), ev.Get("clientY").Float(), full, isDir)
			}))
		p.list.Call("appendChild", row)
	}
}

// open descends into a directory, or hands a file to the viewer. It goes
// through the desk rather than knowing what a viewer is, so a project that
// registers a better viewer gets it here for free.
func (p *Pane) open(full string, isDir bool) {
	if isDir {
		p.chdir(full)
		return
	}
	if _, ok := desk.Lookup("viewer"); !ok {
		return
	}
	if _, err := desk.LaunchOpts("viewer",
		desk.Options{Title: "viewer — " + path.Base(full)}, full); err != nil {
		js.Global().Get("console").Call("warn", "files: "+err.Error())
	}
}

// Close releases the listeners.
func (p *Pane) Close() { p.fns.Release() }

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 3; m /= unit {
		div *= unit
		exp++
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp]), ".0")
}

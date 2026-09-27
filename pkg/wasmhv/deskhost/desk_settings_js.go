//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/desk_settings_js.go c4-wasm-desk
//
// The ☰ settings app: the tab visor's skywire.conf as a form. That file is
// what the desk regenerates skywire.json from on every load, so it is where
// a setting has to go to outlast a reload; an edit to skywire.json alone is
// gone at the next one. The window reads and writes it through the visor's
// skyenv API, the calls `skywire cli visor skyenv` makes, and each field is
// the same flag `skywire autoconfig` takes, so what the form writes is what
// that command would have written.
package deskhost

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/0magnet/desk"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func registerSettingsApp() {
	desk.Register(desk.App{
		Name: "settings", Title: "settings",
		Help:  "this tab's skywire.conf: what every reload generates the visor's config from",
		Width: 820, Height: 600,
		Open: func(_ []string) (desk.Pane, error) {
			return &settingsPane{visorRPC: visorRPC{timeout: 30 * time.Second}}, nil
		},
	})
}

type settingsPane struct {
	visorRPC

	mu     sync.Mutex
	doc    js.Value
	body   js.Value
	status js.Value
	funcs  []js.Func

	rows   []settingRow
	inputs map[string]js.Value // variable → its <input> or <select>
	lines  []settingLine       // for the filter
	folds  []js.Value          // the collapsible sections
}

// settingLine is one rendered row and the text the filter matches.
type settingLine struct {
	el   js.Value
	text string
	set  bool
}

func (p *settingsPane) Mount(el js.Value) error {
	p.doc = js.Global().Get("document")
	root := paneEl(p.doc, "div", paneCSS+";display:flex;flex-direction:column;padding:0", "")
	p.status = paneEl(p.doc, "div", "padding:8px 12px;font-size:12px;color:#9aa3b2;min-height:18px;border-bottom:1px solid #2a2535", "loading…")
	root.Call("appendChild", p.status)
	p.body = paneEl(p.doc, "div", "flex:1;overflow:auto;padding:0 12px 12px", "")
	root.Call("appendChild", p.body)
	el.Call("appendChild", root)
	go p.showForm()
	return nil
}

func (p *settingsPane) Close() {
	p.mu.Lock()
	for _, f := range p.funcs {
		f.Release()
	}
	p.funcs = nil
	p.mu.Unlock()
	p.dropRPC()
}

func (p *settingsPane) keep(f js.Func) js.Func {
	p.mu.Lock()
	p.funcs = append(p.funcs, f)
	p.mu.Unlock()
	return f
}

func (p *settingsPane) setStatus(s string) { p.status.Set("textContent", s) }

func (p *settingsPane) clear() { p.body.Set("textContent", "") }

// showForm loads the file and draws one row per variable.
func (p *settingsPane) showForm() {
	var st visorapi.SkyenvState
	if err := p.call(func(a visorapi.API) (err error) { st, err = a.Skyenv(); return err }); err != nil {
		p.setStatus("settings: " + err.Error())
		return
	}
	p.clear()
	switch {
	case !st.Exists:
		p.setStatus(st.Path + " does not exist yet: it is written the first time the visor starts.")
		return
	case !st.Writable:
		p.setStatus(st.Path + " is read-only to this visor. Change it on the host with `sudo skywire autoconfig --<flag>`.")
	default:
		p.setStatus(st.Path + " · changes apply at the next reload")
	}
	p.rows = settingRows(st)
	p.inputs = map[string]js.Value{}
	p.lines, p.folds = nil, nil

	bar := paneEl(p.doc, "div", "display:flex;flex-wrap:wrap;align-items:center;gap:8px;padding:8px 0;position:sticky;top:0;background:#15121d;z-index:1", "")
	filter := paneEl(p.doc, "input", "flex:1;min-width:160px;padding:6px;font:13px ui-monospace,monospace;background:#0f0d15;color:#e6e9ee;border:1px solid #2a2535", "")
	filter.Set("placeholder", "filter: name, flag or description")
	onlySet := paneEl(p.doc, "input", "", "")
	onlySet.Set("type", "checkbox")
	onlyLbl := paneEl(p.doc, "label", "font-size:12px;color:#9aa3b2;display:flex;align-items:center;gap:4px", "")
	onlyLbl.Call("appendChild", onlySet)
	onlyLbl.Call("appendChild", p.doc.Call("createTextNode", "set in the file only"))
	apply := func(js.Value, []js.Value) any {
		q := strings.ToLower(strings.TrimSpace(filter.Get("value").String()))
		only := onlySet.Get("checked").Bool()
		for _, l := range p.lines {
			show := (q == "" || strings.Contains(l.text, q)) && (!only || l.set)
			// style.display, not the hidden attribute: the row's inline
			// display:flex outranks [hidden].
			display := "none"
			if show {
				display = ""
			}
			l.el.Get("style").Set("display", display)
		}
		// A match inside a collapsed section is still a match.
		for _, d := range p.folds {
			d.Set("open", q != "" || only)
		}
		return nil
	}
	filter.Call("addEventListener", "input", p.keep(js.FuncOf(apply)))
	onlySet.Call("addEventListener", "change", p.keep(js.FuncOf(apply)))
	bar.Call("appendChild", filter)
	bar.Call("appendChild", onlyLbl)
	if st.Writable {
		bar.Call("appendChild", paneButton(p.doc, "Review changes", p.review))
	}
	p.body.Call("appendChild", bar)

	var main, deskRows, hostRows []settingRow
	for _, r := range p.rows {
		switch {
		case r.Desk:
			deskRows = append(deskRows, r)
		case r.HostOnly:
			hostRows = append(hostRows, r)
		default:
			main = append(main, r)
		}
	}
	// Variables the file sets come first: they are what this tab changed.
	sort.SliceStable(main, func(i, j int) bool { return main[i].Set && !main[j].Set })
	for _, r := range main {
		p.body.Call("appendChild", p.rowEl(r, st.Writable))
	}
	p.section("Set by the desk on every load", "the desk passes these to autoconfig each time it starts the visor, so an edit would last one boot", deskRows, false)
	p.section("Host only", "no effect in a browser tab, which cannot listen on a port, open a TUN device or run a separate daemon", hostRows, st.Writable)
}

func (p *settingsPane) section(title, help string, rows []settingRow, editable bool) {
	if len(rows) == 0 {
		return
	}
	d := paneEl(p.doc, "details", "margin-top:14px", "")
	d.Call("appendChild", paneEl(p.doc, "summary", "cursor:pointer;color:#cdd2da", title+" ("+strconv.Itoa(len(rows))+")"))
	d.Call("appendChild", paneEl(p.doc, "div", "font-size:11px;color:#6f7787;margin:4px 0", help))
	for _, r := range rows {
		d.Call("appendChild", p.rowEl(r, editable))
	}
	p.folds = append(p.folds, d)
	p.body.Call("appendChild", d)
}

// rowEl draws one variable: its name and flag, what it does, and a field.
func (p *settingsPane) rowEl(r settingRow, editable bool) js.Value {
	row := paneEl(p.doc, "div", "display:flex;flex-wrap:wrap;gap:4px 12px;align-items:flex-start;padding:8px 0;border-bottom:1px solid #221e2c", "")
	left := paneEl(p.doc, "div", "flex:1 1 220px;min-width:0", "")
	name := paneEl(p.doc, "div", "font:13px ui-monospace,monospace;color:#e6e9ee;word-break:break-all", r.Key)
	if r.Set {
		name.Get("style").Set("color", "#8fd18f")
		name.Set("title", "set in the file")
	}
	left.Call("appendChild", name)
	left.Call("appendChild", paneEl(p.doc, "div", "font-size:12px;color:#9aa3b2", r.Help))
	hint := "--" + r.Flag
	if r.Note != "" {
		hint += " · " + r.Note
	}
	left.Call("appendChild", paneEl(p.doc, "div", "font:11px ui-monospace,monospace;color:#6f7787;word-break:break-word", hint))
	row.Call("appendChild", left)

	field := p.fieldEl(r)
	field.Set("disabled", !editable || r.Desk)
	p.inputs[r.Key] = field
	row.Call("appendChild", field)

	p.lines = append(p.lines, settingLine{el: row, set: r.Set,
		text: strings.ToLower(r.Key + " " + r.Flag + " " + r.Help)})
	return row
}

func (p *settingsPane) fieldEl(r settingRow) js.Value {
	const css = "flex:none;width:260px;max-width:100%;box-sizing:border-box;padding:6px;font:13px ui-monospace,monospace;background:#0f0d15;color:#e6e9ee;border:1px solid #2a2535"
	def := defaultShown(r)
	if r.Format == "bool" {
		sel := paneEl(p.doc, "select", css, "")
		for _, o := range [][2]string{{"", def}, {"true", "on"}, {"false", "off"}} {
			opt := paneEl(p.doc, "option", "", o[1])
			opt.Set("value", o[0])
			sel.Call("appendChild", opt)
		}
		if r.Set {
			sel.Set("value", r.Value)
		}
		return sel
	}
	in := paneEl(p.doc, "input", css, "")
	in.Set("placeholder", def)
	if r.Format == "int" {
		in.Set("inputMode", "numeric")
	}
	if r.Secret || visorapi.SkyenvSecret(r.Key) {
		in.Set("type", "password")
		in.Set("autocomplete", "off")
	}
	in.Set("value", r.Value)
	return in
}

// form reads every field back.
func (p *settingsPane) form() map[string]settingInput {
	out := map[string]settingInput{}
	for key, in := range p.inputs {
		v := in.Get("value").String()
		if in.Get("tagName").String() == "SELECT" && v == "" {
			out[key] = settingInput{Unset: true}
			continue
		}
		out[key] = settingInput{Value: v}
	}
	return out
}

// review shows what Save would change and asks before writing.
func (p *settingsPane) review() {
	edits, changes, err := settingsEdits(p.rows, p.form())
	if err != nil {
		p.setStatus("not saved: " + err.Error())
		return
	}
	if len(changes) == 0 {
		p.setStatus("nothing changed")
		return
	}
	p.clear()
	p.setStatus("Save writes these to skywire.conf; they take effect at the next reload.")
	list := paneEl(p.doc, "div", "font:13px ui-monospace,monospace;margin:10px 0", "")
	for _, c := range changes {
		line := paneEl(p.doc, "div", "padding:4px 0;border-bottom:1px solid #221e2c;word-break:break-all", "")
		line.Call("appendChild", paneEl(p.doc, "span", "color:#e6e9ee", c.Key+"  "))
		line.Call("appendChild", paneEl(p.doc, "span", "color:#c98a8a", c.From))
		line.Call("appendChild", paneEl(p.doc, "span", "color:#6f7787", "  →  "))
		line.Call("appendChild", paneEl(p.doc, "span", "color:#8fd18f", c.To))
		list.Call("appendChild", line)
	}
	p.body.Call("appendChild", list)
	p.body.Call("appendChild", paneButton(p.doc, "Save", func() { p.save(edits) }))
	p.body.Call("appendChild", paneButton(p.doc, "Back", p.showForm))
}

func (p *settingsPane) save(edits visorapi.SkyenvEdits) {
	if err := p.call(func(a visorapi.API) error { _, err := a.SetSkyenv(edits); return err }); err != nil {
		p.setStatus("not saved: " + err.Error())
		return
	}
	p.clear()
	p.setStatus("Saved. The visor runs with the old settings until the page reloads.")
	p.body.Call("appendChild", paneButton(p.doc, "Reload now", func() {
		js.Global().Get("location").Call("reload")
	}))
	p.body.Call("appendChild", paneButton(p.doc, "Back to settings", p.showForm))
}

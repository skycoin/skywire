//go:build js && wasm

package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"html"
	"syscall/js"

	"github.com/0magnet/websh/progressive"
)

// Shipped widgets (PROTOCOL.md): a program not in the tab — on another
// machine, over ssh — sends a widget as a document, and the host runs it in a
// sandboxed iframe. The sandbox allows scripts and nothing else: no
// same-origin access, so the widget can draw and compute but cannot touch
// the page, its storage or its cookies. It talks only to the program, over
// the same line as any widget.

// shipLimit is the most a command may ship, all widgets together.
const shipLimit = 8 << 20

// shipBoot runs first in every shipped widget: it takes the line the host
// transfers in, and gives the document websh.send and websh.onmessage.
// Messages are JSON text on the wire, values in the document.
const shipBoot = `<script>(()=>{let port=null,q=[],h=null;` +
	`addEventListener("message",e=>{if(e.source!==parent||e.data!=="websh-line"||port||!e.ports[0])return;` +
	`port=e.ports[0];port.onmessage=m=>{if(h)try{h(JSON.parse(m.data))}catch(_){}};q.forEach(s=>port.postMessage(s));q=[]});` +
	`window.websh={send(v){const s=JSON.stringify(v);if(port)port.postMessage(s);else q.push(s)},onmessage(f){h=f}};})();</script>`

// shipping is what a command has shipped: finished widgets by name, and the
// ones still arriving.
type shipping struct {
	done map[string]shippedWidget
	part map[string]*bytes.Buffer
	size int
}

// shippedWidget is one finished: a document, or a wasm module.
type shippedWidget struct {
	kind string
	body []byte
}

// ship takes one chunk of widget name; the last one makes it placeable.
func (p *placements) ship(name, meta, chunk string) {
	var m struct {
		Kind string `json:"kind"`
		More bool   `json:"more"`
	}
	mb, err := base64.StdEncoding.DecodeString(meta)
	if err != nil || json.Unmarshal(mb, &m) != nil || (m.Kind != "html" && m.Kind != "wasm") || name == "" {
		return
	}
	b, err := base64.StdEncoding.DecodeString(chunk)
	if err != nil {
		return
	}
	sh := &p.shipped
	if sh.done == nil {
		sh.done, sh.part = map[string]shippedWidget{}, map[string]*bytes.Buffer{}
	}
	if sh.size+len(b) > shipLimit {
		delete(sh.part, name)
		return
	}
	sh.size += len(b)
	part := sh.part[name]
	if part == nil {
		part = &bytes.Buffer{}
		sh.part[name] = part
	}
	part.Write(b)
	if !m.More {
		sh.done[name] = shippedWidget{kind: m.Kind, body: part.Bytes()}
		delete(sh.part, name)
	}
}

// forgetShipped drops what the last command shipped, as it ends.
func (p *placements) forgetShipped() { p.shipped = shipping{} }

// mountShipped fills pl with a shipped widget: a sandboxed iframe holding its
// document, and the other end of the line transferred in once it has loaded.
// Clicks inside it stay inside it, so a shipped widget tells the program what
// was done to it itself, by sending.
func (p *placements) mountShipped(pl *placement, w shippedWidget) {
	ch := js.Global().Get("MessageChannel").New()
	pl.port = ch.Get("port1")
	if pl.d.Events {
		pl.onMsg = js.FuncOf(func(_ js.Value, args []js.Value) any {
			if len(args) == 0 {
				return nil
			}
			d := args[0].Get("data")
			if d.Type() != js.TypeString || !json.Valid([]byte(d.String())) {
				return nil
			}
			p.s.event(pl.id, &progressive.Event{Type: "message", Data: json.RawMessage(d.String())})
			return nil
		})
		pl.port.Set("onmessage", pl.onMsg)
	}
	f := js.Global().Get("document").Call("createElement", "iframe")
	f.Call("setAttribute", "sandbox", "allow-scripts")
	f.Get("style").Set("cssText", "position:absolute;inset:0;width:100%;height:100%;border:0;background:transparent")
	doc := string(w.body)
	if w.kind == "wasm" {
		doc = wasmDoc(w.body)
	}
	f.Set("srcdoc", shipBoot+doc)
	var loaded js.Func
	loaded = js.FuncOf(func(js.Value, []js.Value) any {
		if cw := f.Get("contentWindow"); cw.Truthy() {
			cw.Call("postMessage", "websh-line", "*", js.ValueOf([]any{ch.Get("port2")}))
			if w.kind == "wasm" { // the module itself, handed over rather than copied
				buf := js.Global().Get("Uint8Array").New(len(w.body))
				js.CopyBytesToJS(buf, w.body)
				msg := js.Global().Get("Object").New()
				msg.Set("webshWasm", buf.Get("buffer"))
				cw.Call("postMessage", msg, "*", js.ValueOf([]any{buf.Get("buffer")}))
			}
		}
		loaded.Release()
		return nil
	})
	f.Call("addEventListener", "load", loaded, map[string]any{"once": true})
	pl.el.Call("append", f)
}

// wasmDoc is the document a shipped wasm module runs in: the loader of the
// toolchain that built it (one built by TinyGo imports WASI, one built by
// Go does not), and a runner that starts the module the host hands in. Its
// Go has the page under it as any Go program in a browser does
// (syscall/js, the document), and websh.send and websh.onmessage for its
// line (Go: package widget/inside).
func wasmDoc(module []byte) string {
	loader := wasmExecURL(bytes.Contains(module, []byte("wasi_snapshot_preview1")))
	return "<!doctype html><meta charset=\"utf-8\"><style>html,body{margin:0;height:100%;overflow:hidden}</style>" +
		"<script src=\"" + html.EscapeString(loader) + "\"></script>" +
		"<script>addEventListener(\"message\",async e=>{if(e.source!==parent||!e.data||!e.data.webshWasm)return;" +
		"const go=new Go();const r=await WebAssembly.instantiate(e.data.webshWasm,go.importObject);go.run(r.instance)})</script>"
}

// wasmExecURL is where this page's loader for the toolchain is, as an
// address the sandboxed document can fetch (an absolute one: it has no
// address of its own to resolve against).
func wasmExecURL(tinygo bool) string {
	rel := "wasm_exec.js"
	if a := js.Global().Get("proc"); a.Truthy() && a.Get("assets").Truthy() {
		key := "wasmExecGo"
		if tinygo {
			key = "wasmExecTinyGo"
		}
		if v := a.Get("assets").Get(key); v.Type() == js.TypeString && v.String() != "" {
			rel = v.String()
		} else if !tinygo && a.Get("assets").Get("wasmExec").Type() == js.TypeString {
			rel = a.Get("assets").Get("wasmExec").String()
		}
	}
	return js.Global().Get("URL").New(rel, js.Global().Get("location").Get("href")).Get("href").String()
}
